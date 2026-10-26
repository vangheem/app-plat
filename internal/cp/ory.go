package cp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type identity struct {
	ID     string
	Email  string
	Name   string
	Traits map[string]any
}

type oryClient struct {
	public string
	admin  string
	http   *http.Client
}

func newOry(public, admin string) *oryClient {
	return &oryClient{public: public, admin: admin, http: &http.Client{Timeout: 20 * time.Second}}
}

func (o *oryClient) Login(ctx context.Context, email, password string) (string, identity, error) {
	flow, err := o.flow(ctx, "/self-service/login/api")
	if err != nil {
		return "", identity{}, err
	}
	body := map[string]string{
		"method":              "password",
		"identifier":          email,
		"password":            password,
		"password_identifier": email,
	}
	var resp struct {
		SessionToken string `json:"session_token"`
		Session      struct {
			Identity jsonIdentity `json:"identity"`
		} `json:"session"`
	}
	if err := o.postPublic(ctx, "/self-service/login?flow="+url.QueryEscape(flow), body, &resp); err != nil {
		return "", identity{}, err
	}
	if resp.SessionToken == "" {
		return "", identity{}, fmt.Errorf("kratos did not return a session")
	}
	return resp.SessionToken, resp.Session.Identity.toID(), nil
}

func (o *oryClient) Register(ctx context.Context, email, password, name string) (string, identity, error) {
	flow, err := o.flow(ctx, "/self-service/registration/api")
	if err != nil {
		return "", identity{}, err
	}
	body := map[string]any{
		"method":   "password",
		"password": password,
		"traits":   map[string]string{"email": email, "name": name},
	}
	var resp struct {
		SessionToken string `json:"session_token"`
		Session      struct {
			Identity jsonIdentity `json:"identity"`
		} `json:"session"`
		Identity jsonIdentity `json:"identity"`
	}
	if err := o.postPublic(ctx, "/self-service/registration?flow="+url.QueryEscape(flow), body, &resp); err != nil {
		return "", identity{}, err
	}
	id := resp.Session.Identity
	if id.ID == "" {
		id = resp.Identity
	}
	return resp.SessionToken, id.toID(), nil
}

func (o *oryClient) Whoami(ctx context.Context, token string) (identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.public+"/sessions/whoami", nil)
	if err != nil {
		return identity{}, err
	}
	req.Header.Set("X-Session-Token", token)
	var resp struct {
		Identity jsonIdentity `json:"identity"`
	}
	if err := o.do(req, &resp); err != nil {
		return identity{}, err
	}
	return resp.Identity.toID(), nil
}

func (o *oryClient) CreateIdentity(ctx context.Context, email, password, name string) (identity, error) {
	body := map[string]any{
		"schema_id": "default",
		"traits":    map[string]string{"email": email, "name": name},
		"credentials": map[string]any{
			"password": map[string]any{"config": map[string]string{"password": password}},
		},
	}
	var id jsonIdentity
	err := o.postAdmin(ctx, "/admin/identities", body, &id)
	if err == nil {
		return id.toID(), nil
	}
	if !strings.Contains(err.Error(), "409") && !strings.Contains(strings.ToLower(err.Error()), "conflict") {
		found, ferr := o.FindIdentity(ctx, email)
		if ferr == nil {
			return found, nil
		}
		return identity{}, err
	}
	return o.FindIdentity(ctx, email)
}

func (o *oryClient) FindIdentity(ctx context.Context, email string) (identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.admin+"/admin/identities?credentials_identifier="+url.QueryEscape(email), nil)
	if err != nil {
		return identity{}, err
	}
	var list []jsonIdentity
	if err := o.do(req, &list); err != nil {
		return identity{}, err
	}
	if len(list) == 0 {
		return identity{}, fmt.Errorf("identity %s not found", email)
	}
	return list[0].toID(), nil
}

func (o *oryClient) flow(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.public+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	var resp struct {
		ID string `json:"id"`
	}
	if err := o.do(req, &resp); err != nil {
		return "", err
	}
	if resp.ID == "" {
		return "", fmt.Errorf("empty kratos flow")
	}
	return resp.ID, nil
}

func (o *oryClient) postPublic(ctx context.Context, path string, body any, out any) error {
	return o.post(ctx, o.public+path, body, out)
}

func (o *oryClient) postAdmin(ctx context.Context, path string, body any, out any) error {
	return o.post(ctx, o.admin+path, body, out)
}

func (o *oryClient) post(ctx context.Context, rawURL string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return o.do(req, out)
}

func (o *oryClient) do(req *http.Request, out any) error {
	resp, err := o.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return fmt.Errorf("ory %s: %s", resp.Status, msg)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

type jsonIdentity struct {
	ID     string         `json:"id"`
	Traits map[string]any `json:"traits"`
}

func (j jsonIdentity) toID() identity {
	email, _ := j.Traits["email"].(string)
	name, _ := j.Traits["name"].(string)
	return identity{ID: j.ID, Email: email, Name: name, Traits: j.Traits}
}

type hydraClient struct {
	admin string
	http  *http.Client
}

func newHydra(admin string) *hydraClient {
	return &hydraClient{admin: admin, http: &http.Client{Timeout: 20 * time.Second}}
}

type hydraLogin struct {
	Challenge string
	Client    string
	Subject   string
	Scope     []string
	Skip      bool
}

type hydraConsent struct {
	Challenge string
	Client    string
	Subject   string
	Scope     []string
}

func (h *hydraClient) GetLogin(ctx context.Context, challenge string) (hydraLogin, error) {
	var resp struct {
		Challenge string `json:"challenge"`
		Subject   string `json:"subject"`
		Skip      bool   `json:"skip"`
		Client    struct {
			Name string `json:"client_name"`
		} `json:"client"`
		RequestedScope []string `json:"requested_scope"`
	}
	err := h.get(ctx, "/admin/oauth2/auth/requests/login?login_challenge="+url.QueryEscape(challenge), &resp)
	return hydraLogin{Challenge: resp.Challenge, Client: resp.Client.Name, Subject: resp.Subject, Scope: resp.RequestedScope, Skip: resp.Skip}, err
}

func (h *hydraClient) AcceptLogin(ctx context.Context, challenge, subject string, remember bool) (string, error) {
	var resp struct {
		RedirectTo string `json:"redirect_to"`
	}
	body := map[string]any{"subject": subject, "remember": remember, "remember_for": 3600}
	err := h.put(ctx, "/admin/oauth2/auth/requests/login/accept?login_challenge="+url.QueryEscape(challenge), body, &resp)
	return resp.RedirectTo, err
}

func (h *hydraClient) GetConsent(ctx context.Context, challenge string) (hydraConsent, error) {
	var resp struct {
		Challenge string `json:"challenge"`
		Subject   string `json:"subject"`
		Client    struct {
			Name string `json:"client_name"`
		} `json:"client"`
		RequestedScope []string `json:"requested_scope"`
	}
	err := h.get(ctx, "/admin/oauth2/auth/requests/consent?consent_challenge="+url.QueryEscape(challenge), &resp)
	return hydraConsent{Challenge: resp.Challenge, Client: resp.Client.Name, Subject: resp.Subject, Scope: resp.RequestedScope}, err
}

func (h *hydraClient) AcceptConsent(ctx context.Context, challenge, subject string, scope []string, remember bool) (string, error) {
	var resp struct {
		RedirectTo string `json:"redirect_to"`
	}
	body := map[string]any{
		"grant_scope":  scope,
		"remember":     remember,
		"remember_for": 3600,
		"session": map[string]any{
			"access_token": map[string]any{"sub": subject},
			"id_token":     map[string]any{"sub": subject},
		},
	}
	err := h.put(ctx, "/admin/oauth2/auth/requests/consent/accept?consent_challenge="+url.QueryEscape(challenge), body, &resp)
	return resp.RedirectTo, err
}

type hydraOAuthClient struct {
	ID           string   `json:"client_id"`
	Name         string   `json:"client_name"`
	Secret       string   `json:"client_secret"`
	GrantTypes   []string `json:"grant_types"`
	RedirectURIs []string `json:"redirect_uris"`
	Scope        string   `json:"scope"`
}

func (h *hydraClient) ListClients(ctx context.Context) ([]hydraOAuthClient, error) {
	var out []hydraOAuthClient
	err := h.get(ctx, "/admin/clients", &out)
	if out == nil {
		out = []hydraOAuthClient{}
	}
	return out, err
}

func (h *hydraClient) CreateClient(ctx context.Context, name string, grants, redirects []string, scope string) (hydraOAuthClient, error) {
	if len(grants) == 0 {
		grants = []string{"authorization_code", "refresh_token", "client_credentials"}
	}
	if scope == "" {
		scope = "openid offline apps"
	}
	body := map[string]any{
		"client_name":                 name,
		"grant_types":                 grants,
		"response_types":              []string{"code"},
		"redirect_uris":               redirects,
		"scope":                       scope,
		"token_endpoint_auth_method":  "client_secret_basic",
	}
	var out hydraOAuthClient
	err := h.post(ctx, "/admin/clients", body, &out)
	return out, err
}

func (h *hydraClient) DeleteClient(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, h.admin+"/admin/clients/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	return h.do(req, nil)
}

func (h *hydraClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.admin+path, nil)
	if err != nil {
		return err
	}
	return h.do(req, out)
}

func (h *hydraClient) post(ctx context.Context, path string, body, out any) error {
	return h.write(ctx, http.MethodPost, path, body, out)
}

func (h *hydraClient) put(ctx context.Context, path string, body, out any) error {
	return h.write(ctx, http.MethodPut, path, body, out)
}

func (h *hydraClient) write(ctx context.Context, method, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, h.admin+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return h.do(req, out)
}

func (h *hydraClient) do(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return fmt.Errorf("hydra %s: %s", resp.Status, msg)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
