package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		log.Fatalf("invalid PORT %q", port)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /healthz/", healthz)
	mux.HandleFunc("GET /{$}", home)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func home(w http.ResponseWriter, r *http.Request) {
	checks := map[string]func(context.Context) string{
		"postgres": checkPostgres,
		"blob":     checkBlob,
		"kv":       checkKV,
		"llm":      checkLLM,
	}
	results := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, fn := range checks {
		wg.Add(1)
		go func(name string, fn func(context.Context) string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			val := safe(fn, ctx)
			mu.Lock()
			results[name] = val
			mu.Unlock()
		}(name, fn)
	}
	wg.Wait()
	app := os.Getenv("APP_PLAT_APP_NAME")
	if app == "" {
		app = "unset"
	}
	body := strings.Join([]string{
		"Golang Demo",
		"app: " + app,
		"postgres: " + results["postgres"],
		"blob: " + results["blob"],
		"kv: " + results["kv"],
		"llm: " + results["llm"],
		"",
	}, "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

func safe(fn func(context.Context) string, ctx context.Context) (out string) {
	defer func() {
		if rec := recover(); rec != nil {
			out = "error: " + redact(fmt.Sprint(rec))
		}
	}()
	return fn(ctx)
}

func checkPostgres(ctx context.Context) string {
	dsn := strings.TrimSpace(os.Getenv("APP_PLAT_POSTGRES_URL"))
	if dsn == "" {
		dsn = postgresDSNFromParts()
	}
	if dsn == "" {
		return "error: postgres is not configured"
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "error: " + redact(err.Error())
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return "error: " + redact(err.Error())
	}
	defer conn.Close(context.Background())
	_, err = conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS demo (
			id BIGSERIAL PRIMARY KEY,
			message TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return "error: " + redact(err.Error())
	}
	var id int64
	var message string
	err = conn.QueryRow(ctx, "INSERT INTO demo (message) VALUES ($1) RETURNING id, message", "hello from golang").Scan(&id, &message)
	if err != nil {
		return "error: " + redact(err.Error())
	}
	return fmt.Sprintf("ok id=%d message=%s", id, message)
}

func postgresDSNFromParts() string {
	host := os.Getenv("APP_PLAT_POSTGRES_HOST")
	port := os.Getenv("APP_PLAT_POSTGRES_PORT")
	user := os.Getenv("APP_PLAT_POSTGRES_USER")
	pass := os.Getenv("APP_PLAT_POSTGRES_PASSWORD")
	db := os.Getenv("APP_PLAT_POSTGRES_DATABASE")
	if host == "" || port == "" || user == "" || db == "" {
		return ""
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, pass),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + db,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

func checkBlob(ctx context.Context) string {
	endpoint := strings.TrimSpace(os.Getenv("APP_PLAT_BLOB_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("APP_PLAT_BLOB_BUCKET"))
	access := os.Getenv("APP_PLAT_BLOB_ACCESS_KEY")
	secret := os.Getenv("APP_PLAT_BLOB_SECRET_KEY")
	region := strings.TrimSpace(os.Getenv("APP_PLAT_BLOB_REGION"))
	if region == "" {
		region = "us-east-1"
	}
	if endpoint == "" || bucket == "" || access == "" || secret == "" {
		return "error: blob is not configured"
	}
	host, secure, err := blobHost(endpoint)
	if err != nil {
		return "error: " + redact(err.Error())
	}
	client, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(access, secret, ""),
		Secure:       secure,
		Region:       region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return "error: " + redact(err.Error())
	}
	body := "hello from golang"
	_, err = client.PutObject(ctx, bucket, "demo.txt", strings.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: "text/plain"})
	if err != nil {
		return "error: " + redact(err.Error())
	}
	obj, err := client.GetObject(ctx, bucket, "demo.txt", minio.GetObjectOptions{})
	if err != nil {
		return "error: " + redact(err.Error())
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return "error: " + redact(err.Error())
	}
	return "ok demo.txt=" + string(data)
}

func blobHost(raw string) (string, bool, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, err
	}
	if u.Host == "" {
		return "", false, fmt.Errorf("invalid blob endpoint")
	}
	return u.Host, u.Scheme == "https", nil
}

func checkKV(ctx context.Context) string {
	prefix := os.Getenv("APP_PLAT_KV_PREFIX")
	if prefix == "" {
		return "error: APP_PLAT_KV_PREFIX is unset"
	}
	client, err := kvClient()
	if err != nil {
		return "error: " + redact(err.Error())
	}
	defer client.Close()
	key := prefix + "demo"
	if err := client.Set(ctx, key, "hello from golang", 0).Err(); err != nil {
		return "error: " + redact(err.Error())
	}
	got, err := client.Get(ctx, key).Result()
	if err != nil {
		return "error: " + redact(err.Error())
	}
	return fmt.Sprintf("ok %s=%s", key, got)
}

func kvClient() (*redis.Client, error) {
	if raw := strings.TrimSpace(os.Getenv("APP_PLAT_KV_URL")); raw != "" {
		opt, err := redis.ParseURL(raw)
		if err != nil {
			return nil, err
		}
		opt.DialTimeout = 3 * time.Second
		opt.ReadTimeout = 3 * time.Second
		opt.WriteTimeout = 3 * time.Second
		return redis.NewClient(opt), nil
	}
	host := os.Getenv("APP_PLAT_KV_HOST")
	port := os.Getenv("APP_PLAT_KV_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("kv is not configured")
	}
	return redis.NewClient(&redis.Options{
		Addr:         net.JoinHostPort(host, port),
		Username:     os.Getenv("APP_PLAT_KV_USERNAME"),
		Password:     os.Getenv("APP_PLAT_KV_PASSWORD"),
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}), nil
}

func checkLLM(ctx context.Context) string {
	base := strings.TrimSpace(os.Getenv("APP_PLAT_LLM_BASE_URL"))
	if base == "" {
		return "LLM is not configured"
	}
	key := os.Getenv("APP_PLAT_LLM_API_KEY")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/models", nil)
	if err != nil {
		return "error: " + redact(err.Error())
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "error: " + redact(err.Error())
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Sprintf("error: status %d", resp.StatusCode)
	}
	return fmt.Sprintf("ok status=%d", resp.StatusCode)
}

func redact(msg string) string {
	for _, name := range []string{
		"APP_PLAT_POSTGRES_PASSWORD",
		"APP_PLAT_POSTGRES_URL",
		"APP_PLAT_BLOB_SECRET_KEY",
		"APP_PLAT_BLOB_ACCESS_KEY",
		"APP_PLAT_KV_PASSWORD",
		"APP_PLAT_KV_URL",
		"APP_PLAT_LLM_API_KEY",
	} {
		if val := os.Getenv(name); val != "" {
			msg = strings.ReplaceAll(msg, val, "***")
		}
	}
	return msg
}
