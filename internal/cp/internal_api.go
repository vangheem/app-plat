package cp

import (
	"context"
	"net/http"
	"strings"
)

func (s *Server) internalAuthorize(w http.ResponseWriter, r *http.Request) {
	if !s.internal(w, r) {
		return
	}
	var req struct {
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	allowed, userID, appID, reason := s.authorizeLLM(r.Context(), req.APIKey, req.Model)
	writeJSON(w, http.StatusOK, map[string]any{
		"allowed": allowed, "user_id": userID, "app_id": appID, "reason": reason,
	})
}

func (s *Server) authorizeLLM(ctx context.Context, key, model string) (bool, string, string, string) {
	if !strings.HasPrefix(key, "llmk_") {
		return false, "", "", "invalid llm key"
	}
	res, app, err := s.store.ResourcesByLLMKey(ctx, key)
	if err != nil || !res.LLMEnabled {
		return false, "", "", "llm access is not enabled"
	}
	grant, err := s.store.LatestGrant(ctx, app.ID)
	if err != nil || grant.Status != "approved" {
		return false, app.OwnerID, app.ID, "llm grant is not approved"
	}
	models, _ := s.store.ListModels(ctx, true)
	enabled := false
	for _, m := range models {
		if m.ID == model {
			enabled = true
			break
		}
	}
	if !enabled {
		return false, app.OwnerID, app.ID, "model is not enabled"
	}
	if len(grant.Models) > 0 {
		ok := false
		for _, m := range grant.Models {
			if m == model {
				ok = true
				break
			}
		}
		if !ok {
			return false, app.OwnerID, app.ID, "model is not in the app grant"
		}
	}
	if grant.SpendingCapUSD > 0 {
		spend, _ := s.store.MonthSpend(ctx, "", app.ID)
		if spend >= grant.SpendingCapUSD {
			return false, app.OwnerID, app.ID, "app spending cap reached"
		}
	}
	user, err := s.store.GetUser(ctx, app.OwnerID)
	if err == nil && user.LLMCapUSD > 0 {
		spend, _ := s.store.MonthSpend(ctx, user.ID, "")
		if spend >= user.LLMCapUSD {
			return false, app.OwnerID, app.ID, "user spending cap reached"
		}
	}
	return true, app.OwnerID, app.ID, ""
}

func (s *Server) internalUsage(w http.ResponseWriter, r *http.Request) {
	if !s.internal(w, r) {
		return
	}
	var req struct {
		APIKey           string  `json:"api_key"`
		Model            string  `json:"model"`
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		CostUSD          float64 `json:"cost_usd"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	_, app, err := s.store.ResourcesByLLMKey(r.Context(), req.APIKey)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown llm key")
		return
	}
	if err := s.store.RecordUsage(r.Context(), app.OwnerID, app.ID, req.Model, req.PromptTokens, req.CompletionTokens, req.CostUSD); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) internalModels(w http.ResponseWriter, r *http.Request) {
	if !s.internal(w, r) {
		return
	}
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	res, app, err := s.store.ResourcesByLLMKey(r.Context(), req.APIKey)
	if err != nil || !res.LLMEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		return
	}
	grant, err := s.store.LatestGrant(r.Context(), app.ID)
	if err != nil || grant.Status != "approved" {
		writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		return
	}
	models, err := s.store.ListModels(r.Context(), true)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := []item{}
	allow := map[string]bool{}
	for _, m := range grant.Models {
		allow[m] = true
	}
	for _, m := range models {
		if len(allow) > 0 && !allow[m.ID] {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		out = append(out, item{ID: m.ID, Name: name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

func (s *Server) internalBlobResolve(w http.ResponseWriter, r *http.Request) {
	if !s.internal(w, r) {
		return
	}
	var req struct {
		AccessKey string `json:"access_key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	res, err := s.store.ResourcesByBlobKey(r.Context(), req.AccessKey)
	if err != nil || res.BlobBucket == "" {
		writeErr(w, http.StatusNotFound, "unknown access key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secret_key":  res.BlobSecretKey,
		"bucket":      res.BlobBucket,
		"quota_bytes": res.BlobQuotaBytes,
		"used_bytes":  res.BlobUsedBytes,
	})
}

func (s *Server) internalBlobUsage(w http.ResponseWriter, r *http.Request) {
	if !s.internal(w, r) {
		return
	}
	var req struct {
		AccessKey  string `json:"access_key"`
		DeltaBytes int64  `json:"delta_bytes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.AddBlobUsage(r.Context(), req.AccessKey, req.DeltaBytes); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
