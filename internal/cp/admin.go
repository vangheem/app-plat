package cp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/app-plat/app-plat/internal/store"
	"github.com/app-plat/app-plat/pkg/api"
)

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.User, 0, len(users))
	for _, u := range users {
		out = append(out, toAPIUser(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	var req api.CreateUserRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	if req.Role != "user" && req.Role != "admin" {
		writeErr(w, http.StatusBadRequest, "role must be user or admin")
		return
	}
	id, err := s.kratos.CreateIdentity(r.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	review := true
	if req.LLMRequiresReview != nil {
		review = *req.LLMRequiresReview
	}
	u := store.User{ID: id.ID, Email: req.Email, Name: req.Name, Role: req.Role, LLMRequiresReview: review}
	if err := s.store.UpsertUser(r.Context(), u); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// role/review may already exist from a previous identity; force the requested values
	updated, err := s.store.UpdateUser(r.Context(), u.ID, &req.Role, &review, &req.Name, nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toAPIUser(updated))
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	var req api.UpdateUserRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	u, err := s.store.UpdateUser(r.Context(), r.PathValue("id"), req.Role, req.LLMRequiresReview, req.Name, req.LLMCapUSD)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, toAPIUser(u))
}

func (s *Server) adminListApps(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	apps, err := s.store.ListApps(r.Context(), "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIApps(apps))
}

func (s *Server) adminHealth(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.healthReport(r.Context()))
}

func (s *Server) adminListModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	models, err := s.store.ListModels(r.Context(), false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIModels(models))
}

func (s *Server) listEnabledModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.current(w, r); !ok {
		return
	}
	models, err := s.store.ListModels(r.Context(), true)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIModels(models))
}

func (s *Server) adminSyncModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	if err := s.syncModels(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	models, err := s.store.ListModels(r.Context(), false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIModels(models))
}

func (s *Server) syncModels(ctx context.Context) error {
	if s.cfg.OpenRouterAPIKey == "" {
		return errString("OPENROUTER_API_KEY is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.OpenRouterURL+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.OpenRouterAPIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return errString("openrouter models: " + resp.Status)
	}
	var body struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	existing, _ := s.store.ListModels(ctx, false)
	enabled := map[string]bool{}
	for _, m := range existing {
		enabled[m.ID] = m.Enabled
	}
	for _, m := range body.Data {
		if m.ID == "" {
			continue
		}
		if err := s.store.UpsertModel(ctx, store.LLMModel{
			ID: m.ID, Name: m.Name, Enabled: enabled[m.ID], ContextLength: m.ContextLength,
			PromptPrice: m.Pricing.Prompt, CompletionPrice: m.Pricing.Completion,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) adminSetModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	var req api.SetModelsRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	for _, m := range req.Models {
		if err := s.store.SetModelEnabled(r.Context(), m.ID, m.Enabled); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listMyLLM(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	grants, err := s.store.ListGrants(r.Context(), a.User.ID, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIGrants(grants))
}

func (s *Server) createLLM(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	var req api.CreateLLMRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	app, err := s.store.GetApp(r.Context(), req.AppID)
	if err != nil {
		app, err = s.store.GetAppBySlug(r.Context(), req.AppID)
	}
	if err != nil || (app.OwnerID != a.User.ID && a.User.Role != "admin") {
		writeErr(w, http.StatusNotFound, "app not found")
		return
	}
	status := "pending"
	var decided *time.Time
	if !a.User.LLMRequiresReview || a.User.Role == "admin" {
		status = "approved"
		now := time.Now()
		decided = &now
	}
	g := store.Grant{
		ID: newID("llm_"), UserID: app.OwnerID, AppID: app.ID, Status: status,
		SpendingCapUSD: req.SpendingCapUSD, Models: req.Models, CreatedAt: time.Now(), DecidedAt: decided,
	}
	if err := s.store.InsertGrant(r.Context(), g); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, _ := s.store.GetResources(r.Context(), app.ID)
	res.AppID = app.ID
	res.LLMEnabled = status == "approved"
	if res.LLMEnabled && res.LLMAPIKey == "" {
		res.LLMAPIKey = newSecret("llmk_")
	}
	if !res.LLMEnabled {
		res.LLMEnabled = false
	}
	_ = s.store.SaveResources(r.Context(), res)
	if app.Status == "running" {
		app.Status = "deploying"
		_ = s.store.UpdateApp(r.Context(), app)
	}
	s.Wake()
	saved, _ := s.store.GetGrant(r.Context(), g.ID)
	writeJSON(w, http.StatusCreated, toAPIGrant(saved))
}

func (s *Server) adminListLLM(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	grants, err := s.store.ListGrants(r.Context(), "", r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIGrants(grants))
}

func (s *Server) adminDecideLLM(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	var req api.DecideLLMRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	g, err := s.store.GetGrant(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "request not found")
		return
	}
	status := "denied"
	if req.Approve {
		status = "approved"
	}
	if err := s.store.SetGrantStatus(r.Context(), g.ID, status, req.Note); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, _ := s.store.GetResources(r.Context(), g.AppID)
	res.AppID = g.AppID
	res.LLMEnabled = req.Approve
	if req.Approve && res.LLMAPIKey == "" {
		res.LLMAPIKey = newSecret("llmk_")
	}
	_ = s.store.SaveResources(r.Context(), res)
	if app, err := s.store.GetApp(r.Context(), g.AppID); err == nil && app.Status == "running" {
		app.Status = "deploying"
		_ = s.store.UpdateApp(r.Context(), app)
	}
	s.Wake()
	saved, _ := s.store.GetGrant(r.Context(), g.ID)
	writeJSON(w, http.StatusOK, toAPIGrant(saved))
}

func (s *Server) adminUsage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	rows, err := s.store.UsageSummary(r.Context(), r.URL.Query().Get("user_id"), r.URL.Query().Get("app_id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.UsageRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.UsageRow{
			UserID: row.UserID, UserEmail: row.UserEmail, AppID: row.AppID, AppSlug: row.AppSlug,
			Model: row.Model, PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens,
			CostUSD: row.CostUSD, Requests: row.Requests,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminSetCap(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	var req api.SetCapRequest
	if err := readJSON(r, &req); err != nil || req.SpendingCapUSD == nil {
		writeErr(w, http.StatusBadRequest, "spending_cap_usd required")
		return
	}
	if req.UserID != "" {
		if _, err := s.store.UpdateUser(r.Context(), req.UserID, nil, nil, nil, req.SpendingCapUSD); err != nil {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
	}
	if req.AppID != "" {
		g, err := s.store.LatestGrant(r.Context(), req.AppID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "no llm grant for app")
			return
		}
		_, err = s.store.Pool.Exec(r.Context(), `UPDATE llm_grants SET spending_cap_usd=$2 WHERE id=$1`, g.ID, *req.SpendingCapUSD)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminListOAuth(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	clients, err := s.hydra.ListClients(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]api.OAuthClient, 0, len(clients))
	for _, c := range clients {
		out = append(out, api.OAuthClient{ID: c.ID, Name: c.Name, GrantTypes: c.GrantTypes, RedirectURIs: c.RedirectURIs, Scope: c.Scope})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminCreateOAuth(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	var req api.CreateOAuthClientRequest
	if err := readJSON(r, &req); err != nil || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	c, err := s.hydra.CreateClient(r.Context(), req.Name, req.GrantTypes, req.RedirectURIs, req.Scope)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, api.OAuthClient{
		ID: c.ID, Name: c.Name, Secret: c.Secret, GrantTypes: c.GrantTypes, RedirectURIs: c.RedirectURIs, Scope: c.Scope,
	})
}

func (s *Server) adminDeleteOAuth(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	if err := s.hydra.DeleteClient(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) oauthLoginInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.current(w, r); !ok {
		return
	}
	info, err := s.hydra.GetLogin(r.Context(), r.URL.Query().Get("challenge"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.OAuthLoginInfo{
		Challenge: info.Challenge, ClientName: info.Client, Subject: info.Subject, RequestedScope: info.Scope, Skip: info.Skip,
	})
}

func (s *Server) oauthAcceptLogin(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	var req api.OAuthAcceptRequest
	if err := readJSON(r, &req); err != nil || req.Challenge == "" {
		writeErr(w, http.StatusBadRequest, "challenge required")
		return
	}
	redir, err := s.hydra.AcceptLogin(r.Context(), req.Challenge, a.User.ID, req.Remember)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.OAuthRedirect{RedirectTo: redir})
}

func (s *Server) oauthConsentInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.current(w, r); !ok {
		return
	}
	info, err := s.hydra.GetConsent(r.Context(), r.URL.Query().Get("challenge"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.OAuthConsentInfo{
		Challenge: info.Challenge, ClientName: info.Client, RequestedScope: info.Scope, Subject: info.Subject,
	})
}

func (s *Server) oauthAcceptConsent(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	var req api.OAuthAcceptRequest
	if err := readJSON(r, &req); err != nil || req.Challenge == "" {
		writeErr(w, http.StatusBadRequest, "challenge required")
		return
	}
	scope := req.GrantScope
	if len(scope) == 0 {
		info, err := s.hydra.GetConsent(r.Context(), req.Challenge)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		scope = info.Scope
	}
	redir, err := s.hydra.AcceptConsent(r.Context(), req.Challenge, a.User.ID, scope, req.Remember)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.OAuthRedirect{RedirectTo: redir})
}

func toAPIModels(models []store.LLMModel) []api.LLMModel {
	out := make([]api.LLMModel, 0, len(models))
	for _, m := range models {
		out = append(out, api.LLMModel{
			ID: m.ID, Name: m.Name, Enabled: m.Enabled, ContextLength: m.ContextLength,
			PromptPrice: m.PromptPrice, CompletionPrice: m.CompletionPrice,
		})
	}
	return out
}

func toAPIGrant(g store.Grant) api.LLMRequest {
	decided := ""
	if g.DecidedAt != nil {
		decided = ts(*g.DecidedAt)
	}
	return api.LLMRequest{
		ID: g.ID, UserID: g.UserID, UserEmail: g.UserEmail, AppID: g.AppID, AppSlug: g.AppSlug,
		Status: g.Status, SpendingCapUSD: g.SpendingCapUSD, Models: g.Models, Note: g.Note,
		CreatedAt: ts(g.CreatedAt), DecidedAt: decided,
	}
}

func toAPIGrants(gs []store.Grant) []api.LLMRequest {
	out := make([]api.LLMRequest, 0, len(gs))
	for _, g := range gs {
		out = append(out, toAPIGrant(g))
	}
	return out
}

type errString string

func (e errString) Error() string { return string(e) }
