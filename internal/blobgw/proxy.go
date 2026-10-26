package blobgw

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7/pkg/signer"
)

type creds struct {
	Secret string
	Bucket string
	Quota  int64
	Used   int64
	Until  time.Time
}

type Proxy struct {
	ControlPlane string
	Token        string
	Upstream     string
	AdminAccess  string
	AdminSecret  string
	Region       string
	Log          *slog.Logger
	http         *http.Client

	mu    sync.Mutex
	cache map[string]creds
}

func (p *Proxy) Handler() http.Handler {
	if p.http == nil {
		p.http = &http.Client{Timeout: 60 * time.Second}
	}
	if p.cache == nil {
		p.cache = map[string]creds{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", p.serve)
	return mux
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	body := rawBody
	access := accessKey(r.Header.Get("Authorization"))
	if access == "" {
		http.Error(w, "missing credentials", http.StatusUnauthorized)
		return
	}
	c, err := p.resolve(r.Context(), access)
	if err != nil {
		http.Error(w, "unknown access key", http.StatusForbidden)
		return
	}
	if !verify(r, rawBody, access, c.Secret, p.Region) {
		p.Log.Info("signature mismatch", "method", r.Method, "path", r.URL.Path, "access", access)
		http.Error(w, "signature mismatch", http.StatusForbidden)
		return
	}
	bucket, _ := splitBucket(r.URL.Path)
	if bucket != "" && bucket != c.Bucket {
		http.Error(w, "bucket not allowed", http.StatusForbidden)
		return
	}
	if decoded, ok := decodeAWSChunked(rawBody); ok {
		body = decoded
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		if c.Quota > 0 && c.Used+int64(len(body)) > c.Quota {
			http.Error(w, "blob quota exceeded", http.StatusForbidden)
			return
		}
	}
	up, err := url.Parse(p.Upstream)
	if err != nil {
		http.Error(w, "bad upstream", http.StatusBadGateway)
		return
	}
	target := up.Scheme + "://" + up.Host + r.URL.RequestURI()
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		out.Header.Set("Content-Type", ct)
	}
	sum := sha256.Sum256(body)
	out.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sum[:]))
	out.ContentLength = int64(len(body))
	signed := signer.SignV4(*out, p.AdminAccess, p.AdminSecret, "", p.Region)
	resp, err := p.http.Do(signed)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		p.Log.Info("upstream denied", "method", r.Method, "path", r.URL.Path, "status", resp.StatusCode, "body", string(snippet))
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(snippet), resp.Body))
	}
	if resp.StatusCode < 300 && (r.Method == http.MethodPut || r.Method == http.MethodPost) && len(body) > 0 {
		_ = p.report(r.Context(), access, int64(len(body)))
	}
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *Proxy) resolve(ctx context.Context, access string) (creds, error) {
	p.mu.Lock()
	if c, ok := p.cache[access]; ok && time.Now().Before(c.Until) {
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()
	buf, _ := json.Marshal(map[string]string{"access_key": access})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.ControlPlane, "/")+"/internal/blob/resolve", bytes.NewReader(buf))
	if err != nil {
		return creds{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", p.Token)
	resp, err := p.http.Do(req)
	if err != nil {
		return creds{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return creds{}, io.ErrUnexpectedEOF
	}
	var body struct {
		Secret string `json:"secret_key"`
		Bucket string `json:"bucket"`
		Quota  int64  `json:"quota_bytes"`
		Used   int64  `json:"used_bytes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return creds{}, err
	}
	c := creds{Secret: body.Secret, Bucket: body.Bucket, Quota: body.Quota, Used: body.Used, Until: time.Now().Add(15 * time.Second)}
	p.mu.Lock()
	p.cache[access] = c
	p.mu.Unlock()
	return c, nil
}

func (p *Proxy) report(ctx context.Context, access string, delta int64) error {
	buf, _ := json.Marshal(map[string]any{"access_key": access, "delta_bytes": delta})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.ControlPlane, "/")+"/internal/blob/usage", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", p.Token)
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	p.mu.Lock()
	if c, ok := p.cache[access]; ok {
		c.Used += delta
		p.cache[access] = c
	}
	p.mu.Unlock()
	return nil
}

func accessKey(header string) string {
	const marker = "Credential="
	i := strings.Index(header, marker)
	if i < 0 {
		return ""
	}
	rest := header[i+len(marker):]
	if slash := strings.Index(rest, "/"); slash > 0 {
		return rest[:slash]
	}
	return ""
}

func splitBucket(path string) (string, string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", ""
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}


