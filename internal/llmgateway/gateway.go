// Package llmgateway is an OpenAI-compatible proxy.
// It authorizes llmk_ keys with the control plane and forwards chat
// completions to OpenRouter. API keys are never logged.
package llmgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	pathAuthorize = "/internal/llm/authorize"
	pathUsage     = "/internal/llm/usage"
	pathModels    = "/internal/llm/models"
	maxBody       = 8 << 20
	maxUpstream   = 32 << 20
)

// Config is process configuration. Tokens are never written to logs.
type Config struct {
	ListenAddr        string
	ControlPlaneURL   string
	InternalToken     string
	OpenRouterAPIKey  string
	OpenRouterBaseURL string
}

// ConfigFromEnv reads gateway settings. INTERNAL_TOKEN and OPENROUTER_API_KEY
// have no defaults; the others do.
func ConfigFromEnv() Config {
	return Config{
		ListenAddr:        envOr("LISTEN_ADDR", ":8083"),
		ControlPlaneURL:   strings.TrimRight(envOr("CONTROL_PLANE_URL", "http://control-plane:8080"), "/"),
		InternalToken:     strings.TrimSpace(os.Getenv("INTERNAL_TOKEN")),
		OpenRouterAPIKey:  strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")),
		OpenRouterBaseURL: strings.TrimRight(envOr("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"), "/"),
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Gateway proxies authorized chat completions and model lists.
type Gateway struct {
	cfg    Config
	cp     *http.Client
	up     *http.Client
	logger *log.Logger
}

// New returns a gateway. Callers may replace the logger in tests.
func New(cfg Config) *Gateway {
	cfg.ControlPlaneURL = strings.TrimRight(cfg.ControlPlaneURL, "/")
	cfg.OpenRouterBaseURL = strings.TrimRight(cfg.OpenRouterBaseURL, "/")
	return &Gateway{
		cfg: cfg,
		cp: &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		up:     &http.Client{Timeout: 3 * time.Minute},
		logger: log.Default(),
	}
}

// Handler serves /healthz, /v1/models, and /v1/chat/completions.
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			g.healthz(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			g.models(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			g.chat(w, r)
		case r.URL.Path == "/healthz" || r.URL.Path == "/v1/models" || r.URL.Path == "/v1/chat/completions":
			g.writeErr(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed", "")
		default:
			g.writeErr(w, http.StatusNotFound, "not found", "invalid_request_error", "not_found", "")
		}
	})
}

func (g *Gateway) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	key, err := bearerKey(r)
	if err != nil {
		g.writeErr(w, http.StatusUnauthorized, err.Error(), "authentication_error", "invalid_api_key", "")
		return
	}
	if g.cfg.InternalToken == "" || g.cfg.ControlPlaneURL == "" {
		g.writeErr(w, http.StatusInternalServerError, "gateway is not configured", "server_error", "not_configured", key)
		return
	}
	status, raw, err := g.postCP(r.Context(), pathModels, map[string]string{"api_key": key})
	if err != nil {
		g.logf("models control-plane call failed: %s", redact(err.Error(), key))
		g.writeErr(w, http.StatusBadGateway, "control plane unavailable", "server_error", "upstream_error", key)
		return
	}
	if status != http.StatusOK {
		g.writeCPError(w, status, raw, key)
		return
	}
	var listed struct {
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		g.writeErr(w, http.StatusBadGateway, "control plane returned invalid models", "server_error", "upstream_error", key)
		return
	}
	data := make([]openAIModel, 0, len(listed.Models))
	now := time.Now().Unix()
	for _, m := range listed.Models {
		data = append(data, openAIModel{
			ID:      m.ID,
			Object:  "model",
			Created: now,
			OwnedBy: "openrouter",
			Name:    m.Name,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(openAIModelList{Object: "list", Data: data})
}

func (g *Gateway) chat(w http.ResponseWriter, r *http.Request) {
	key, err := bearerKey(r)
	if err != nil {
		g.writeErr(w, http.StatusUnauthorized, err.Error(), "authentication_error", "invalid_api_key", "")
		return
	}
	if g.cfg.InternalToken == "" || g.cfg.ControlPlaneURL == "" {
		g.writeErr(w, http.StatusInternalServerError, "gateway is not configured", "server_error", "not_configured", key)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		g.writeErr(w, http.StatusBadRequest, "failed to read request body", "invalid_request_error", "invalid_body", key)
		return
	}
	if len(body) > maxBody {
		g.writeErr(w, http.StatusRequestEntityTooLarge, "request body too large", "invalid_request_error", "body_too_large", key)
		return
	}
	model, err := requestModel(body)
	if err != nil {
		g.writeErr(w, http.StatusBadRequest, err.Error(), "invalid_request_error", "model_required", key)
		return
	}
	allowed, reason, status, raw, err := g.authorize(r.Context(), key, model)
	if err != nil {
		g.logf("authorize failed model=%s: %s", model, redact(err.Error(), key))
		g.writeErr(w, http.StatusBadGateway, "control plane unavailable", "server_error", "upstream_error", key)
		return
	}
	if status != http.StatusOK {
		g.writeCPError(w, status, raw, key)
		return
	}
	if !allowed {
		if reason == "" {
			reason = "not allowed"
		}
		g.writeErr(w, http.StatusForbidden, reason, "permission_error", "forbidden", key)
		return
	}
	if g.cfg.OpenRouterAPIKey == "" {
		g.writeErr(w, http.StatusServiceUnavailable, "OPENROUTER_API_KEY is not configured", "server_error", "not_configured", key)
		return
	}
	if strings.ContainsAny(g.cfg.OpenRouterAPIKey, "\r\n") {
		g.writeErr(w, http.StatusServiceUnavailable, "OPENROUTER_API_KEY is invalid", "server_error", "not_configured", key)
		return
	}
	upStatus, upHeader, upBody, err := g.proxy(r.Context(), body)
	if err != nil {
		g.logf("upstream failed model=%s: %s", model, redact(err.Error(), key))
		g.writeErr(w, http.StatusBadGateway, "upstream unavailable", "server_error", "upstream_error", key)
		return
	}
	if upStatus == http.StatusOK {
		if err := g.reportUsage(key, model, upHeader, upBody); err != nil {
			g.logf("usage report failed model=%s: %s", model, redact(err.Error(), key))
		}
	}
	ct := upHeader.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	if cost := upHeader.Get("x-openrouter-cost"); cost != "" {
		w.Header().Set("x-openrouter-cost", cost)
	}
	w.WriteHeader(upStatus)
	_, _ = w.Write(upBody)
	g.logf("chat model=%s status=%d", model, upStatus)
}

func (g *Gateway) authorize(ctx context.Context, apiKey, model string) (bool, string, int, []byte, error) {
	status, raw, err := g.postCP(ctx, pathAuthorize, map[string]string{
		"api_key": apiKey,
		"model":   model,
	})
	if err != nil {
		return false, "", 0, nil, err
	}
	if status != http.StatusOK {
		return false, "", status, raw, nil
	}
	var out struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, "", 0, raw, fmt.Errorf("invalid authorize response")
	}
	return out.Allowed, out.Reason, status, raw, nil
}

func (g *Gateway) proxy(ctx context.Context, body []byte) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	url := g.cfg.OpenRouterBaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.cfg.OpenRouterAPIKey)
	req.Header.Set("User-Agent", "app-plat-llm-gateway")
	resp, err := g.up.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstream+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(raw) > maxUpstream {
		return 0, nil, nil, fmt.Errorf("upstream response too large")
	}
	return resp.StatusCode, resp.Header.Clone(), raw, nil
}

func (g *Gateway) reportUsage(apiKey, model string, hdr http.Header, body []byte) error {
	prompt, completion, bodyCost := usageTokens(body)
	cost := headerCost(hdr)
	if cost == 0 {
		cost = bodyCost
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, raw, err := g.postCP(ctx, pathUsage, map[string]any{
		"api_key":           apiKey,
		"model":             model,
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"cost_usd":          cost,
	})
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("control plane status %d", status)
	}
	_ = raw
	return nil
}

func (g *Gateway) postCP(ctx context.Context, path string, in any) (int, []byte, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.ControlPlaneURL+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", g.cfg.InternalToken)
	resp, err := g.cp.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func (g *Gateway) writeCPError(w http.ResponseWriter, status int, raw []byte, secret string) {
	msg := cpMessage(raw, status)
	if status >= 500 {
		g.writeErr(w, http.StatusBadGateway, "control plane error", "server_error", "upstream_error", secret)
		return
	}
	if status < 400 {
		status = http.StatusBadGateway
	}
	typ := "invalid_request_error"
	if status == http.StatusUnauthorized {
		typ = "authentication_error"
	}
	if status == http.StatusForbidden {
		typ = "permission_error"
	}
	g.writeErr(w, status, msg, typ, "control_plane", secret)
}

func (g *Gateway) writeErr(w http.ResponseWriter, status int, message, typ, code, secret string) {
	message = redact(message, secret)
	message = redact(message, g.cfg.InternalToken)
	message = redact(message, g.cfg.OpenRouterAPIKey)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    typ,
			"code":    code,
		},
	})
}

func (g *Gateway) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	msg = redact(msg, g.cfg.InternalToken)
	msg = redact(msg, g.cfg.OpenRouterAPIKey)
	if g.logger == nil {
		log.Print(msg)
		return
	}
	g.logger.Print(msg)
}

type openAIModelList struct {
	Object string        `json:"object"`
	Data   []openAIModel `json:"data"`
}

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	Name    string `json:"name,omitempty"`
}

func bearerKey(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", fmt.Errorf("missing bearer token")
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if strings.ContainsAny(tok, "\r\n") || !strings.HasPrefix(tok, "llmk_") {
		return "", fmt.Errorf("invalid api key")
	}
	return tok, nil
}

func requestModel(body []byte) (string, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", fmt.Errorf("model is required")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", fmt.Errorf("request body must be JSON")
	}
	raw, ok := probe["model"]
	if !ok {
		return "", fmt.Errorf("model is required")
	}
	var model string
	if err := json.Unmarshal(raw, &model); err != nil {
		return "", fmt.Errorf("model is required")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	return model, nil
}

func usageTokens(body []byte) (int, int, float64) {
	var parsed struct {
		Usage *struct {
			PromptTokens     float64 `json:"prompt_tokens"`
			CompletionTokens float64 `json:"completion_tokens"`
			Cost             float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Usage == nil {
		return 0, 0, 0
	}
	return roundNonNeg(parsed.Usage.PromptTokens), roundNonNeg(parsed.Usage.CompletionTokens), parsed.Usage.Cost
}

func roundNonNeg(v float64) int {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return int(math.Round(v))
}

func headerCost(h http.Header) float64 {
	if h == nil {
		return 0
	}
	raw := strings.TrimSpace(h.Get("x-openrouter-cost"))
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0
	}
	return n
}

func cpMessage(raw []byte, status int) string {
	var body struct {
		Error  string `json:"error"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(raw, &body) == nil {
		if body.Error != "" {
			return body.Error
		}
		if body.Reason != "" {
			return body.Reason
		}
	}
	msg := strings.TrimSpace(string(raw))
	if msg == "" {
		return http.StatusText(status)
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

func redact(msg, secret string) string {
	if msg == "" || secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "[redacted]")
}
