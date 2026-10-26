package blobgw

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func verify(orig *http.Request, body []byte, access, secret, region string) bool {
	auth := orig.Header.Get("Authorization")
	parts := map[string]string{}
	for _, piece := range strings.Split(auth, ",") {
		piece = strings.TrimSpace(piece)
		k, v, ok := strings.Cut(piece, "=")
		if !ok {
			continue
		}
		parts[k] = v
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		return false
	}
	signedHeaders := parts["SignedHeaders"]
	got := parts["Signature"]
	amzDate := orig.Header.Get("X-Amz-Date")
	if signedHeaders == "" || got == "" || amzDate == "" {
		return false
	}
	t, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return false
	}
	payloadHash := orig.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" || payloadHash == "UNSIGNED-PAYLOAD" {
		payloadHash = "UNSIGNED-PAYLOAD"
	} else if !strings.HasPrefix(payloadHash, "STREAMING-") {
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != payloadHash {
			return false
		}
	}
	canonHeaders := canonicalHeaders(orig, strings.Split(signedHeaders, ";"))
	canonURI := encodePath(orig.URL.Path)
	query := orig.URL.Query().Encode()
	query = strings.ReplaceAll(query, "+", "%20")
	canonical := strings.Join([]string{
		orig.Method,
		canonURI,
		query,
		canonHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := t.Format("20060102") + "/" + region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := hmacSHA([]byte("AWS4"+secret), t.Format("20060102"))
	key = hmacSHA(key, region)
	key = hmacSHA(key, "s3")
	key = hmacSHA(key, "aws4_request")
	want := hex.EncodeToString(hmacSHA(key, stringToSign))
	return hmac.Equal([]byte(want), []byte(got))
}

func canonicalHeaders(r *http.Request, names []string) string {
	var b strings.Builder
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		name = strings.ToLower(strings.TrimSpace(name))
		b.WriteString(name)
		b.WriteByte(':')
		if name == "host" {
			host := r.Host
			if host == "" {
				host = r.URL.Host
			}
			b.WriteString(host)
		} else {
			vals := r.Header.Values(http.CanonicalHeaderKey(name))
			for i, v := range vals {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(strings.Join(strings.Fields(v), " "))
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func encodePath(p string) string {
	if p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func decodeAWSChunked(raw []byte) ([]byte, bool) {
	if !bytes.Contains(raw, []byte("chunk-signature=")) {
		return nil, false
	}
	var out bytes.Buffer
	rest := raw
	for {
		line, next, ok := bytes.Cut(rest, []byte("\r\n"))
		if !ok {
			return nil, false
		}
		sizeHex, _, _ := bytes.Cut(line, []byte(";"))
		n, err := strconv.ParseInt(string(sizeHex), 16, 64)
		if err != nil {
			return nil, false
		}
		rest = next
		if n == 0 {
			return out.Bytes(), true
		}
		if int64(len(rest)) < n+2 {
			return nil, false
		}
		out.Write(rest[:n])
		rest = rest[n:]
		if !bytes.HasPrefix(rest, []byte("\r\n")) {
			return nil, false
		}
		rest = rest[2:]
	}
}

func hmacSHA(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(data))
	return m.Sum(nil)
}
