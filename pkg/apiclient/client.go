package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/app-plat/app-plat/pkg/api"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) Login(ctx context.Context, email, password string) (api.AuthResponse, error) {
	var out api.AuthResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/login", api.LoginRequest{Email: email, Password: password}, &out)
	return out, err
}

func (c *Client) Register(ctx context.Context, email, password, name string) (api.AuthResponse, error) {
	var out api.AuthResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/register", api.RegisterRequest{Email: email, Password: password, Name: name}, &out)
	return out, err
}

func (c *Client) Me(ctx context.Context) (api.User, error) {
	var out api.User
	err := c.do(ctx, http.MethodGet, "/api/v1/me", nil, &out)
	return out, err
}

func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/v1/logout", map[string]string{}, nil)
}

func (c *Client) ListAPIKeys(ctx context.Context) ([]api.APIKey, error) {
	var out []api.APIKey
	err := c.do(ctx, http.MethodGet, "/api/v1/api-keys", nil, &out)
	return out, err
}

func (c *Client) CreateAPIKey(ctx context.Context, name string) (api.APIKey, error) {
	var out api.APIKey
	err := c.do(ctx, http.MethodPost, "/api/v1/api-keys", api.CreateAPIKeyRequest{Name: name}, &out)
	return out, err
}

func (c *Client) RevokeAPIKey(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/api-keys/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListApps(ctx context.Context) ([]api.App, error) {
	var out []api.App
	err := c.do(ctx, http.MethodGet, "/api/v1/apps", nil, &out)
	return out, err
}

func (c *Client) CreateApp(ctx context.Context, req api.CreateAppRequest) (api.App, error) {
	var out api.App
	err := c.do(ctx, http.MethodPost, "/api/v1/apps", req, &out)
	return out, err
}

func (c *Client) GetApp(ctx context.Context, id string) (api.App, error) {
	var out api.App
	err := c.do(ctx, http.MethodGet, "/api/v1/apps/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) UpdateApp(ctx context.Context, id string, req api.UpdateAppRequest) (api.App, error) {
	var out api.App
	err := c.do(ctx, http.MethodPatch, "/api/v1/apps/"+url.PathEscape(id), req, &out)
	return out, err
}

func (c *Client) DeleteApp(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/apps/"+url.PathEscape(id), nil, nil)
}

func (c *Client) RebuildApp(ctx context.Context, id string) (api.App, error) {
	var out api.App
	err := c.do(ctx, http.MethodPost, "/api/v1/apps/"+url.PathEscape(id)+"/rebuild", map[string]string{}, &out)
	return out, err
}

func (c *Client) UploadSource(ctx context.Context, id, filename string, r io.Reader) (api.App, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("archive", filename)
	if err != nil {
		return api.App{}, err
	}
	if _, err := io.Copy(part, r); err != nil {
		return api.App{}, err
	}
	if err := w.Close(); err != nil {
		return api.App{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/apps/"+url.PathEscape(id)+"/source", &body)
	if err != nil {
		return api.App{}, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	c.auth(req)
	resp, err := c.http().Do(req)
	if err != nil {
		return api.App{}, err
	}
	defer resp.Body.Close()
	var out api.App
	if err := decode(resp, &out); err != nil {
		return api.App{}, err
	}
	return out, nil
}

func (c *Client) ListBuilds(ctx context.Context, id string) ([]api.Build, error) {
	var out []api.Build
	err := c.do(ctx, http.MethodGet, "/api/v1/apps/"+url.PathEscape(id)+"/builds", nil, &out)
	return out, err
}

func (c *Client) GetEnv(ctx context.Context, id string) ([]api.EnvVar, error) {
	var out []api.EnvVar
	err := c.do(ctx, http.MethodGet, "/api/v1/apps/"+url.PathEscape(id)+"/env", nil, &out)
	return out, err
}

func (c *Client) PutEnv(ctx context.Context, id string, vars []api.EnvVar) error {
	return c.do(ctx, http.MethodPut, "/api/v1/apps/"+url.PathEscape(id)+"/env", api.PutEnvRequest{Vars: vars}, nil)
}

func (c *Client) LLMPolicy(ctx context.Context) (api.LLMPolicy, error) {
	var out api.LLMPolicy
	err := c.do(ctx, http.MethodGet, "/api/v1/me/llm", nil, &out)
	return out, err
}

func (c *Client) ListLLMModels(ctx context.Context) ([]api.LLMModel, error) {
	var out []api.LLMModel
	err := c.do(ctx, http.MethodGet, "/api/v1/llm/models", nil, &out)
	return out, err
}

func (c *Client) ListLLMRequests(ctx context.Context) ([]api.LLMRequest, error) {
	var out []api.LLMRequest
	err := c.do(ctx, http.MethodGet, "/api/v1/llm/requests", nil, &out)
	return out, err
}

func (c *Client) CreateLLMRequest(ctx context.Context, req api.CreateLLMRequest) (api.LLMRequest, error) {
	var out api.LLMRequest
	err := c.do(ctx, http.MethodPost, "/api/v1/llm/requests", req, &out)
	return out, err
}

func (c *Client) AdminListUsers(ctx context.Context) ([]api.User, error) {
	var out []api.User
	err := c.do(ctx, http.MethodGet, "/api/v1/admin/users", nil, &out)
	return out, err
}

func (c *Client) AdminCreateUser(ctx context.Context, req api.CreateUserRequest) (api.User, error) {
	var out api.User
	err := c.do(ctx, http.MethodPost, "/api/v1/admin/users", req, &out)
	return out, err
}

func (c *Client) AdminUpdateUser(ctx context.Context, id string, req api.UpdateUserRequest) (api.User, error) {
	var out api.User
	err := c.do(ctx, http.MethodPatch, "/api/v1/admin/users/"+url.PathEscape(id), req, &out)
	return out, err
}

func (c *Client) AdminListApps(ctx context.Context) ([]api.App, error) {
	var out []api.App
	err := c.do(ctx, http.MethodGet, "/api/v1/admin/apps", nil, &out)
	return out, err
}

func (c *Client) AdminHealth(ctx context.Context) (api.HealthReport, error) {
	var out api.HealthReport
	err := c.do(ctx, http.MethodGet, "/api/v1/admin/health", nil, &out)
	return out, err
}

func (c *Client) AdminListModels(ctx context.Context) ([]api.LLMModel, error) {
	var out []api.LLMModel
	err := c.do(ctx, http.MethodGet, "/api/v1/admin/llm/models", nil, &out)
	return out, err
}

func (c *Client) AdminSyncModels(ctx context.Context) ([]api.LLMModel, error) {
	var out []api.LLMModel
	err := c.do(ctx, http.MethodPost, "/api/v1/admin/llm/models/sync", map[string]string{}, &out)
	return out, err
}

func (c *Client) AdminSetModels(ctx context.Context, models []api.ModelToggle) error {
	return c.do(ctx, http.MethodPut, "/api/v1/admin/llm/models", api.SetModelsRequest{Models: models}, nil)
}

func (c *Client) AdminListLLMRequests(ctx context.Context, status string) ([]api.LLMRequest, error) {
	path := "/api/v1/admin/llm/requests"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var out []api.LLMRequest
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) AdminDecideLLM(ctx context.Context, id string, approve bool, note string) (api.LLMRequest, error) {
	var out api.LLMRequest
	err := c.do(ctx, http.MethodPost, "/api/v1/admin/llm/requests/"+url.PathEscape(id)+"/decision", api.DecideLLMRequest{Approve: approve, Note: note}, &out)
	return out, err
}

func (c *Client) AdminUsage(ctx context.Context, userID, appID string) ([]api.UsageRow, error) {
	q := url.Values{}
	if userID != "" {
		q.Set("user_id", userID)
	}
	if appID != "" {
		q.Set("app_id", appID)
	}
	path := "/api/v1/admin/llm/usage"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out []api.UsageRow
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) AdminSetCap(ctx context.Context, req api.SetCapRequest) error {
	return c.do(ctx, http.MethodPut, "/api/v1/admin/llm/caps", req, nil)
}

func (c *Client) AdminListOAuthClients(ctx context.Context) ([]api.OAuthClient, error) {
	var out []api.OAuthClient
	err := c.do(ctx, http.MethodGet, "/api/v1/admin/oauth/clients", nil, &out)
	return out, err
}

func (c *Client) AdminCreateOAuthClient(ctx context.Context, req api.CreateOAuthClientRequest) (api.OAuthClient, error) {
	var out api.OAuthClient
	err := c.do(ctx, http.MethodPost, "/api/v1/admin/oauth/clients", req, &out)
	return out, err
}

func (c *Client) AdminDeleteOAuthClient(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/admin/oauth/clients/"+url.PathEscape(id), nil, nil)
}

func (c *Client) OAuthLoginInfo(ctx context.Context, challenge string) (api.OAuthLoginInfo, error) {
	var out api.OAuthLoginInfo
	err := c.do(ctx, http.MethodGet, "/api/v1/oauth2/login?challenge="+url.QueryEscape(challenge), nil, &out)
	return out, err
}

func (c *Client) OAuthAcceptLogin(ctx context.Context, req api.OAuthAcceptRequest) (api.OAuthRedirect, error) {
	var out api.OAuthRedirect
	err := c.do(ctx, http.MethodPost, "/api/v1/oauth2/login/accept", req, &out)
	return out, err
}

func (c *Client) OAuthConsentInfo(ctx context.Context, challenge string) (api.OAuthConsentInfo, error) {
	var out api.OAuthConsentInfo
	err := c.do(ctx, http.MethodGet, "/api/v1/oauth2/consent?challenge="+url.QueryEscape(challenge), nil, &out)
	return out, err
}

func (c *Client) OAuthAcceptConsent(ctx context.Context, req api.OAuthAcceptRequest) (api.OAuthRedirect, error) {
	var out api.OAuthRedirect
	err := c.do(ctx, http.MethodPost, "/api/v1/oauth2/consent/accept", req, &out)
	return out, err
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) auth(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *Client) do(ctx context.Context, method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.auth(req)
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var eb api.ErrorBody
		_ = json.Unmarshal(raw, &eb)
		if eb.Error == "" {
			eb.Error = strings.TrimSpace(string(raw))
			if eb.Error == "" {
				eb.Error = resp.Status
			}
		}
		return fmt.Errorf("%s", eb.Error)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
