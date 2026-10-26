package cp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/app-plat/app-plat/internal/config"
	"github.com/app-plat/app-plat/internal/store"
	"github.com/app-plat/app-plat/pkg/api"
	"github.com/app-plat/app-plat/pkg/appyaml"
	"github.com/jackc/pgx/v5"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

type Server struct {
	cfg    config.Config
	store  *store.Store
	kratos *oryClient
	hydra  *hydraClient
	log    *slog.Logger
	wake   chan struct{}
	mu     sync.Mutex
	dyn    dynamic.Interface
	mapper *restmapper.DeferredDiscoveryRESTMapper
}

func New(cfg config.Config, st *store.Store, log *slog.Logger) *Server {
	return &Server{
		cfg:    cfg,
		store:  st,
		kratos: newOry(cfg.KratosPublicURL, cfg.KratosAdminURL),
		hydra:  newHydra(cfg.HydraAdminURL),
		log:    log,
		wake:   make(chan struct{}, 1),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /api/v1/login", s.login)
	mux.HandleFunc("POST /api/v1/register", s.register)
	mux.HandleFunc("POST /api/v1/logout", s.logout)
	mux.HandleFunc("GET /api/v1/me", s.me)
	mux.HandleFunc("GET /api/v1/me/llm", s.llmPolicy)
	mux.HandleFunc("GET /api/v1/api-keys", s.listKeys)
	mux.HandleFunc("POST /api/v1/api-keys", s.createKey)
	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", s.revokeKey)
	mux.HandleFunc("GET /api/v1/apps", s.listApps)
	mux.HandleFunc("POST /api/v1/apps", s.createApp)
	mux.HandleFunc("GET /api/v1/apps/{id}", s.getApp)
	mux.HandleFunc("PATCH /api/v1/apps/{id}", s.updateApp)
	mux.HandleFunc("DELETE /api/v1/apps/{id}", s.deleteApp)
	mux.HandleFunc("POST /api/v1/apps/{id}/source", s.uploadSource)
	mux.HandleFunc("POST /api/v1/apps/{id}/rebuild", s.rebuild)
	mux.HandleFunc("GET /api/v1/apps/{id}/builds", s.listBuilds)
	mux.HandleFunc("GET /api/v1/apps/{id}/env", s.getEnv)
	mux.HandleFunc("PUT /api/v1/apps/{id}/env", s.putEnv)
	mux.HandleFunc("GET /api/v1/llm/models", s.listEnabledModels)
	mux.HandleFunc("GET /api/v1/llm/requests", s.listMyLLM)
	mux.HandleFunc("POST /api/v1/llm/requests", s.createLLM)
	mux.HandleFunc("GET /api/v1/admin/users", s.adminListUsers)
	mux.HandleFunc("POST /api/v1/admin/users", s.adminCreateUser)
	mux.HandleFunc("PATCH /api/v1/admin/users/{id}", s.adminUpdateUser)
	mux.HandleFunc("GET /api/v1/admin/apps", s.adminListApps)
	mux.HandleFunc("GET /api/v1/admin/health", s.adminHealth)
	mux.HandleFunc("GET /api/v1/admin/llm/models", s.adminListModels)
	mux.HandleFunc("POST /api/v1/admin/llm/models/sync", s.adminSyncModels)
	mux.HandleFunc("PUT /api/v1/admin/llm/models", s.adminSetModels)
	mux.HandleFunc("GET /api/v1/admin/llm/requests", s.adminListLLM)
	mux.HandleFunc("POST /api/v1/admin/llm/requests/{id}/decision", s.adminDecideLLM)
	mux.HandleFunc("GET /api/v1/admin/llm/usage", s.adminUsage)
	mux.HandleFunc("PUT /api/v1/admin/llm/caps", s.adminSetCap)
	mux.HandleFunc("GET /api/v1/admin/oauth/clients", s.adminListOAuth)
	mux.HandleFunc("POST /api/v1/admin/oauth/clients", s.adminCreateOAuth)
	mux.HandleFunc("DELETE /api/v1/admin/oauth/clients/{id}", s.adminDeleteOAuth)
	mux.HandleFunc("GET /api/v1/oauth2/login", s.oauthLoginInfo)
	mux.HandleFunc("POST /api/v1/oauth2/login/accept", s.oauthAcceptLogin)
	mux.HandleFunc("GET /api/v1/oauth2/consent", s.oauthConsentInfo)
	mux.HandleFunc("POST /api/v1/oauth2/consent/accept", s.oauthAcceptConsent)
	mux.HandleFunc("POST /internal/llm/authorize", s.internalAuthorize)
	mux.HandleFunc("POST /internal/llm/usage", s.internalUsage)
	mux.HandleFunc("POST /internal/llm/models", s.internalModels)
	mux.HandleFunc("POST /internal/blob/resolve", s.internalBlobResolve)
	mux.HandleFunc("POST /internal/blob/usage", s.internalBlobUsage)
	return mux
}

func (s *Server) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

type actor struct {
	User  store.User
	Token string
}

func (s *Server) current(w http.ResponseWriter, r *http.Request) (actor, bool) {
	h := r.Header.Get("Authorization")
	token := strings.TrimPrefix(h, "Bearer ")
	if token == "" || token == h {
		writeErr(w, http.StatusUnauthorized, "missing bearer token")
		return actor{}, false
	}
	if strings.HasPrefix(token, "apk_") {
		prefix := token
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}
		u, err := s.store.LookupAPIKey(r.Context(), prefix, hashKey(token))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid api key")
			return actor{}, false
		}
		return actor{User: u, Token: token}, true
	}
	id, err := s.kratos.Whoami(r.Context(), token)
	if err != nil || id.ID == "" {
		writeErr(w, http.StatusUnauthorized, "invalid session")
		return actor{}, false
	}
	u, err := s.store.GetUser(r.Context(), id.ID)
	if err != nil {
		u = store.User{ID: id.ID, Email: id.Email, Name: id.Name, Role: "user", LLMRequiresReview: true}
		_ = s.store.UpsertUser(r.Context(), u)
	}
	return actor{User: u, Token: token}, true
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request) (actor, bool) {
	a, ok := s.current(w, r)
	if !ok {
		return a, false
	}
	if a.User.Role != "admin" {
		writeErr(w, http.StatusForbidden, "admin required")
		return a, false
	}
	return a, true
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Internal-Token") != s.cfg.InternalToken || s.cfg.InternalToken == "" {
		writeErr(w, http.StatusUnauthorized, "invalid internal token")
		return false
	}
	return true
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req api.LoginRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	token, id, err := s.kratos.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	u, err := s.store.GetUser(r.Context(), id.ID)
	if err != nil {
		u = store.User{ID: id.ID, Email: id.Email, Name: id.Name, Role: "user", LLMRequiresReview: true}
		if err := s.store.UpsertUser(r.Context(), u); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, api.AuthResponse{SessionToken: token, User: toAPIUser(u)})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req api.RegisterRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "email and password required")
		return
	}
	token, id, err := s.kratos.Register(r.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := store.User{ID: id.ID, Email: req.Email, Name: req.Name, Role: "user", LLMRequiresReview: true}
	if err := s.store.UpsertUser(r.Context(), u); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.AuthResponse{SessionToken: token, User: toAPIUser(u)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toAPIUser(a.User))
}

func (s *Server) llmPolicy(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	spend, _ := s.store.MonthSpend(r.Context(), a.User.ID, "")
	writeJSON(w, http.StatusOK, api.LLMPolicy{
		RequiresReview: a.User.LLMRequiresReview,
		UserCapUSD:     a.User.LLMCapUSD,
		MonthSpendUSD:  spend,
	})
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	keys, err := s.store.ListAPIKeys(r.Context(), a.User.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.APIKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, api.APIKey{ID: k.ID, Name: k.Name, Prefix: k.Prefix, CreatedAt: ts(k.CreatedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	var req api.CreateAPIKeyRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" {
		req.Name = "default"
	}
	plain := newSecret("apk_")
	prefix := plain[:12]
	k := store.APIKey{ID: newID("key_"), UserID: a.User.ID, Name: req.Name, Prefix: prefix, KeyHash: hashKey(plain)}
	if err := s.store.InsertAPIKey(r.Context(), k); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, api.APIKey{ID: k.ID, Name: k.Name, Prefix: prefix, CreatedAt: ts(time.Now()), Key: plain})
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	if err := s.store.RevokeAPIKey(r.Context(), a.User.ID, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "api key not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	owner := a.User.ID
	if a.User.Role == "admin" && r.URL.Query().Get("all") == "1" {
		owner = ""
	}
	apps, err := s.store.ListApps(r.Context(), owner)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAPIApps(apps))
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	var req api.CreateAppRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AppYAML != "" {
		f, err := appyaml.Parse(req.AppYAML)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Name == "" {
			req.Name = f.Name
		}
		if req.Port == 0 {
			req.Port = f.Port
		}
		if len(req.Persistence) == 0 {
			req.Persistence = f.Persistence
		}
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	if req.Port == 0 {
		req.Port = 8080
	}
	pers, err := appyaml.NormalizePersistence(req.Persistence)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	slug := slugify(req.Name)
	for i := 2; ; i++ {
		taken, err := s.store.SlugExists(r.Context(), slug)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !taken {
			break
		}
		slug = slugify(req.Name) + "-" + itoa(i)
		if i > 50 {
			writeErr(w, http.StatusConflict, "could not allocate slug")
			return
		}
	}
	app := store.App{
		ID: newID("app_"), OwnerID: a.User.ID, Name: req.Name, Slug: slug, Port: req.Port,
		Persistence: pers, Status: "created", PublicURL: s.cfg.PublicURL(slug),
	}
	if err := s.store.CreateApp(r.Context(), app); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Wake()
	created, _ := s.store.GetApp(r.Context(), app.ID)
	writeJSON(w, http.StatusCreated, toAPIApp(created))
}

func (s *Server) ownedApp(w http.ResponseWriter, r *http.Request, a actor) (store.App, bool) {
	id := r.PathValue("id")
	app, err := s.store.GetApp(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		app, err = s.store.GetAppBySlug(r.Context(), id)
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "app not found")
		return app, false
	}
	if app.OwnerID != a.User.ID && a.User.Role != "admin" {
		writeErr(w, http.StatusForbidden, "not your app")
		return app, false
	}
	return app, true
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	app, ok := s.ownedApp(w, r, a)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toAPIApp(app))
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	app, ok := s.ownedApp(w, r, a)
	if !ok {
		return
	}
	var req api.UpdateAppRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name != nil {
		app.Name = *req.Name
	}
	if req.Port != nil {
		app.Port = *req.Port
	}
	if req.Persistence != nil {
		pers, err := appyaml.NormalizePersistence(req.Persistence)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		app.Persistence = pers
	}
	if app.Status == "running" {
		app.Status = "deploying"
	}
	if err := s.store.UpdateApp(r.Context(), app); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Wake()
	writeJSON(w, http.StatusOK, toAPIApp(app))
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	app, ok := s.ownedApp(w, r, a)
	if !ok {
		return
	}
	app.Status = "deleting"
	if err := s.store.UpdateApp(r.Context(), app); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Wake()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uploadSource(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	app, ok := s.ownedApp(w, r, a)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "expected multipart archive")
		return
	}
	file, hdr, err := r.FormFile("archive")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "archive file required")
		return
	}
	defer file.Close()
	if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, "sources"), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ext := ".tar.gz"
	if strings.HasSuffix(strings.ToLower(hdr.Filename), ".zip") {
		ext = ".zip"
	}
	dest := filepath.Join(s.cfg.DataDir, "sources", app.ID+ext)
	out, err := os.Create(dest)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.Close()
	if yamlRaw, err := readAppYAML(dest); err == nil && strings.TrimSpace(yamlRaw) != "" {
		if f, err := appyaml.Parse(yamlRaw); err == nil {
			if f.Port != 0 {
				app.Port = f.Port
			}
			if len(f.Persistence) > 0 {
				app.Persistence = f.Persistence
			}
			if f.Name != "" && app.Name == "" {
				app.Name = f.Name
			}
		}
	}
	app.SourcePath = dest
	app.Status = "building"
	app.Error = ""
	if err := s.store.UpdateApp(r.Context(), app); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Wake()
	writeJSON(w, http.StatusAccepted, toAPIApp(app))
}

func (s *Server) rebuild(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	app, ok := s.ownedApp(w, r, a)
	if !ok {
		return
	}
	if app.SourcePath == "" {
		writeErr(w, http.StatusBadRequest, "upload source first")
		return
	}
	app.Status = "building"
	app.Error = ""
	if err := s.store.UpdateApp(r.Context(), app); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Wake()
	writeJSON(w, http.StatusAccepted, toAPIApp(app))
}

func (s *Server) listBuilds(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	app, ok := s.ownedApp(w, r, a)
	if !ok {
		return
	}
	builds, err := s.store.ListBuilds(r.Context(), app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.Build, 0, len(builds))
	for _, b := range builds {
		out = append(out, api.Build{ID: b.ID, AppID: b.AppID, Status: b.Status, Image: b.Image, Log: b.Log, Error: b.Error, CreatedAt: ts(b.CreatedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getEnv(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	app, ok := s.ownedApp(w, r, a)
	if !ok {
		return
	}
	vars, err := s.store.GetEnv(r.Context(), app.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]api.EnvVar, 0, len(vars))
	for _, v := range vars {
		val := v.Value
		if v.Secret {
			val = ""
		}
		out = append(out, api.EnvVar{Key: v.Key, Value: val, Secret: v.Secret})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putEnv(w http.ResponseWriter, r *http.Request) {
	a, ok := s.current(w, r)
	if !ok {
		return
	}
	app, ok := s.ownedApp(w, r, a)
	if !ok {
		return
	}
	var req api.PutEnvRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	prev, _ := s.store.GetEnv(r.Context(), app.ID)
	prevMap := map[string]store.EnvVar{}
	for _, v := range prev {
		prevMap[v.Key] = v
	}
	vars := make([]store.EnvVar, 0, len(req.Vars))
	seen := map[string]bool{}
	for _, v := range req.Vars {
		key := strings.TrimSpace(v.Key)
		if key == "" || seen[key] {
			continue
		}
		if !envNameOK(key) {
			writeErr(w, http.StatusBadRequest, "invalid env name "+key)
			return
		}
		seen[key] = true
		val := v.Value
		if v.Secret && val == "" {
			if old, ok := prevMap[key]; ok {
				val = old.Value
			}
		}
		vars = append(vars, store.EnvVar{Key: key, Value: val, Secret: v.Secret})
	}
	if err := s.store.PutEnv(r.Context(), app.ID, vars); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if app.Status == "running" {
		app.Status = "deploying"
		_ = s.store.UpdateApp(r.Context(), app)
	}
	s.Wake()
	w.WriteHeader(http.StatusNoContent)
}

func envNameOK(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func toAPIUser(u store.User) api.User {
	return api.User{
		ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role,
		LLMRequiresReview: u.LLMRequiresReview, LLMCapUSD: u.LLMCapUSD, CreatedAt: ts(u.CreatedAt),
	}
}

func toAPIApp(a store.App) api.App {
	return api.App{
		ID: a.ID, OwnerID: a.OwnerID, OwnerEmail: a.OwnerEmail, Name: a.Name, Slug: a.Slug,
		Port: a.Port, Persistence: a.Persistence, Status: a.Status, Image: a.Image,
		DetectedType: a.DetectedType, PublicURL: a.PublicURL, BuildLogTail: tail(a.BuildLog, 4000),
		Error: a.Error, CreatedAt: ts(a.CreatedAt), UpdatedAt: ts(a.UpdatedAt),
	}
}

func toAPIApps(apps []store.App) []api.App {
	out := make([]api.App, 0, len(apps))
	for _, a := range apps {
		out = append(out, toAPIApp(a))
	}
	return out
}

func (s *Server) Bootstrap(ctx context.Context) error {
	var last error
	for i := 0; i < 30; i++ {
		id, err := s.kratos.CreateIdentity(ctx, s.cfg.AdminEmail, s.cfg.AdminPassword, "Admin")
		if err != nil {
			last = err
			s.log.Info("waiting for kratos", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		u, err := s.store.GetUser(ctx, id.ID)
		if err != nil {
			u = store.User{ID: id.ID, Email: s.cfg.AdminEmail, Name: "Admin", Role: "admin", LLMRequiresReview: false}
		}
		u.Role = "admin"
		u.Email = s.cfg.AdminEmail
		if u.Name == "" {
			u.Name = "Admin"
		}
		if err := s.store.UpsertUser(ctx, u); err != nil {
			return err
		}
		s.log.Info("admin ready", "email", s.cfg.AdminEmail)
		return nil
	}
	return last
}
