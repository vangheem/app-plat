package llmgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestGatewayProxiesAuthorizedChat(t *testing.T) {
	const (
		userKey    = "llmk_test_key"
		internal   = "internal-secret"
		upstream   = "sk-or-test"
		model      = "openai/gpt-4o-mini"
		wantBody   = `{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
		upResponse = `{"id":"cmpl","model":"openai/gpt-4o-mini","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4}}`
	)
	var mu sync.Mutex
	var authorizeBody []byte
	var usageBody []byte
	var upstreamBody []byte
	var upstreamAuth string
	upstreamHits := 0
	usageHits := 0

	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != internal {
			t.Errorf("internal token = %q", r.Header.Get("X-Internal-Token"))
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("control plane received Authorization")
		}
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/internal/llm/authorize":
			authorizeBody = raw
			var req struct {
				APIKey string `json:"api_key"`
				Model  string `json:"model"`
			}
			_ = json.Unmarshal(raw, &req)
			if req.Model == "blocked" {
				_ = json.NewEncoder(w).Encode(map[string]any{"allowed": false, "reason": "monthly cap exceeded"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"allowed": true, "user_id": "u1", "app_id": "a1", "reason": ""})
		case "/internal/llm/usage":
			usageHits++
			usageBody = raw
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cp.Close()

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		upstreamHits++
		upstreamBody = append([]byte(nil), raw...)
		upstreamAuth = r.Header.Get("Authorization")
		mu.Unlock()
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Errorf("upstream path %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %s", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-openrouter-cost", "0.25")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upResponse))
	}))
	defer up.Close()

	var logs bytes.Buffer
	g := New(Config{
		ControlPlaneURL:   cp.URL,
		InternalToken:     internal,
		OpenRouterAPIKey:  upstream,
		OpenRouterBaseURL: up.URL + "/api/v1",
	})
	g.logger = log.New(&logs, "", 0)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(wantBody))
	req.Header.Set("Authorization", "Bearer "+userKey)
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != upResponse {
		t.Fatalf("body %s", rec.Body.String())
	}
	mu.Lock()
	gotUpstream := append([]byte(nil), upstreamBody...)
	gotAuth := upstreamAuth
	gotAuthorize := append([]byte(nil), authorizeBody...)
	gotUsage := append([]byte(nil), usageBody...)
	gotUsageHits := usageHits
	gotUpstreamHits := upstreamHits
	mu.Unlock()
	if !bytes.Equal(gotUpstream, []byte(wantBody)) {
		t.Fatalf("upstream body %s", gotUpstream)
	}
	if gotAuth != "Bearer "+upstream {
		t.Fatalf("upstream auth %s", gotAuth)
	}
	var authReq struct {
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal(gotAuthorize, &authReq); err != nil {
		t.Fatal(err)
	}
	if authReq.APIKey != userKey || authReq.Model != model {
		t.Fatalf("authorize %#v", authReq)
	}
	var usage struct {
		APIKey           string  `json:"api_key"`
		Model            string  `json:"model"`
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		CostUSD          float64 `json:"cost_usd"`
	}
	if err := json.Unmarshal(gotUsage, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.APIKey != userKey || usage.Model != model || usage.PromptTokens != 3 || usage.CompletionTokens != 4 || usage.CostUSD != 0.25 {
		t.Fatalf("usage %#v", usage)
	}
	if gotUsageHits != 1 || gotUpstreamHits != 1 {
		t.Fatalf("hits usage=%d upstream=%d", gotUsageHits, gotUpstreamHits)
	}
	logText := logs.String()
	for _, secret := range []string{userKey, internal, upstream} {
		if strings.Contains(logText, secret) {
			t.Fatalf("log leaked secret %q: %s", secret, logText)
		}
	}

	denied := httptest.NewRecorder()
	dreq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"blocked","messages":[]}`))
	dreq.Header.Set("Authorization", "Bearer "+userKey)
	g.Handler().ServeHTTP(denied, dreq)
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "monthly cap exceeded") {
		t.Fatalf("denied %d %s", denied.Code, denied.Body.String())
	}
	mu.Lock()
	after := upstreamHits
	mu.Unlock()
	if after != gotUpstreamHits {
		t.Fatal("denied request was proxied")
	}
}

func TestGatewayModelsAndHealth(t *testing.T) {
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/llm/models" || r.Header.Get("X-Internal-Token") != "tok" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		var req struct {
			APIKey string `json:"api_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.APIKey != "llmk_models" {
			http.Error(w, "bad key", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{{"id": "openai/gpt-4o-mini", "name": "GPT-4o mini"}},
		})
	}))
	defer cp.Close()
	g := New(Config{ControlPlaneURL: cp.URL, InternalToken: "tok"})

	health := httptest.NewRecorder()
	g.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != 200 || health.Body.String() != "ok" {
		t.Fatalf("health %d %q", health.Code, health.Body.String())
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer llmk_models")
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var listed struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
			Name   string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Object != "list" || len(listed.Data) != 1 || listed.Data[0].ID != "openai/gpt-4o-mini" || listed.Data[0].Object != "model" || listed.Data[0].Name != "GPT-4o mini" {
		t.Fatalf("%#v", listed)
	}

	unauth := httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	bad.Header.Set("Authorization", "Bearer apk_nope")
	g.Handler().ServeHTTP(unauth, bad)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", unauth.Code)
	}
}
