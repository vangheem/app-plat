package adminui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/app-plat/app-plat/pkg/api"
	"github.com/app-plat/app-plat/pkg/apiclient"
)

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	p := s.loadDash(r, user, c)
	if wantsFragment(r, "live") {
		s.render(w, http.StatusOK, "dash-live", p)
		return
	}
	s.render(w, http.StatusOK, "dashboard", p)
}

func (s *Server) loadDash(r *http.Request, user api.User, c *apiclient.Client) page {
	p := s.authed(user, "Dashboard", "/")
	applyNotice(r, &p)
	var flashes []string
	apps, err := c.AdminListApps(r.Context())
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	users, err := c.AdminListUsers(r.Context())
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	health, err := c.AdminHealth(r.Context())
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	pending, err := c.AdminListLLMRequests(r.Context(), "pending")
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	p.Apps = apps
	p.AppCount = len(apps)
	p.Users = users
	p.UserCount = len(users)
	p.Health = health.Components
	p.Pending = pending
	p.PendingCount = len(pending)
	p.Flash = strings.Join(flashes, " ")
	return p
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	s.renderUsers(w, r, user, c, http.StatusOK, "")
}

func (s *Server) renderUsers(w http.ResponseWriter, r *http.Request, user api.User, c *apiclient.Client, status int, flash string) {
	p := s.authed(user, "Users", "/users")
	applyNotice(r, &p)
	p.Flash = flash
	p.Email = r.FormValue("email")
	p.Name = r.FormValue("name")
	if role := r.FormValue("role"); role != "" {
		p.FormRole = role
	}
	users, err := c.AdminListUsers(r.Context())
	if err != nil {
		if p.Flash != "" {
			p.Flash += " "
		}
		p.Flash += err.Error()
		if status == http.StatusOK {
			status = http.StatusBadGateway
		}
	}
	p.Users = users
	s.render(w, status, "users", p)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderUsers(w, r, user, c, http.StatusBadRequest, "Invalid form.")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")
	role := r.FormValue("role")
	if role == "" {
		role = "user"
	}
	if email == "" || password == "" {
		s.renderUsers(w, r, user, c, http.StatusBadRequest, "Email and password are required.")
		return
	}
	if role != "user" && role != "admin" {
		s.renderUsers(w, r, user, c, http.StatusBadRequest, "Role must be user or admin.")
		return
	}
	review := r.FormValue("llm_requires_review") == "1"
	if _, err := c.AdminCreateUser(r.Context(), api.CreateUserRequest{
		Email:             email,
		Password:          password,
		Name:              name,
		Role:              role,
		LLMRequiresReview: &review,
	}); err != nil {
		s.renderUsers(w, r, user, c, http.StatusBadGateway, err.Error())
		return
	}
	http.Redirect(w, r, "/users?notice=created", http.StatusSeeOther)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderUsers(w, r, user, c, http.StatusBadRequest, "Invalid form.")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	role := r.FormValue("role")
	if role != "user" && role != "admin" {
		s.renderUsers(w, r, user, c, http.StatusBadRequest, "Role must be user or admin.")
		return
	}
	review := r.FormValue("llm_requires_review") == "1"
	capRaw := strings.TrimSpace(r.FormValue("llm_cap_usd"))
	cap, err := strconv.ParseFloat(capRaw, 64)
	if err != nil || cap < 0 {
		s.renderUsers(w, r, user, c, http.StatusBadRequest, "LLM cap must be a number zero or greater. Zero means no cap.")
		return
	}
	if _, err := c.AdminUpdateUser(r.Context(), r.PathValue("id"), api.UpdateUserRequest{
		Role:              &role,
		Name:              &name,
		LLMRequiresReview: &review,
		LLMCapUSD:         &cap,
	}); err != nil {
		s.renderUsers(w, r, user, c, http.StatusBadGateway, err.Error())
		return
	}
	http.Redirect(w, r, "/users?notice=updated", http.StatusSeeOther)
}

func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	p := s.authed(user, "Apps", "/apps")
	apps, err := c.AdminListApps(r.Context())
	if err != nil {
		p.Flash = err.Error()
		s.render(w, http.StatusBadGateway, "apps", p)
		return
	}
	p.Apps = apps
	s.render(w, http.StatusOK, "apps", p)
}

func (s *Server) appDetail(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if wantsFragment(r, "status") {
		s.renderStatusFragment(w, r, c, id)
		return
	}
	app, err := c.GetApp(r.Context(), id)
	if err != nil {
		p := s.authed(user, "App", "/apps")
		p.Flash = err.Error()
		code := http.StatusBadGateway
		if err.Error() == "app not found" {
			code = http.StatusNotFound
			p.Title = "App not found"
		}
		s.render(w, code, "message", p)
		return
	}
	builds, berr := c.ListBuilds(r.Context(), app.ID)
	title := app.Name
	if title == "" {
		title = "App"
	}
	p := s.authed(user, title, "/apps")
	if berr != nil {
		p.Flash = berr.Error()
	}
	p.App = app
	p.Builds = builds
	p.Log = buildLog(app, builds)
	s.render(w, http.StatusOK, "app-detail", p)
}

func (s *Server) renderStatusFragment(w http.ResponseWriter, r *http.Request, c *apiclient.Client, id string) {
	app, err := c.GetApp(r.Context(), id)
	if err != nil {
		s.render(w, http.StatusOK, "status-panel", page{App: api.App{ID: id, Status: "error", Error: err.Error()}})
		return
	}
	builds, _ := c.ListBuilds(r.Context(), app.ID)
	s.render(w, http.StatusOK, "status-panel", page{App: app, Builds: builds, Log: buildLog(app, builds)})
}

func (s *Server) llm(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	s.renderLLM(w, r, user, c, http.StatusOK, "")
}

func (s *Server) renderLLM(w http.ResponseWriter, r *http.Request, user api.User, c *apiclient.Client, status int, flash string) {
	p := s.authed(user, "LLM", "/llm")
	applyNotice(r, &p)
	p.Flash = flash
	p.FilterUser = r.URL.Query().Get("user_id")
	p.FilterApp = r.URL.Query().Get("app_id")
	p.CapUser = r.FormValue("user_id")
	p.CapApp = r.FormValue("app_id")
	p.CapAmount = r.FormValue("spending_cap_usd")
	var flashes []string
	if flash != "" {
		flashes = append(flashes, flash)
	}
	models, err := c.AdminListModels(r.Context())
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	pending, err := c.AdminListLLMRequests(r.Context(), "pending")
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	usage, err := c.AdminUsage(r.Context(), p.FilterUser, p.FilterApp)
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	p.Models = models
	p.EnabledCount = enabledCount(models)
	p.Requests = pending
	p.Usage = usage
	p.Flash = strings.Join(flashes, " ")
	if status == http.StatusOK && len(flashes) > 0 && flash == "" {
		status = http.StatusBadGateway
	}
	s.render(w, status, "llm", p)
}

func (s *Server) syncModels(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if _, err := c.AdminSyncModels(r.Context()); err != nil {
		s.renderLLM(w, r, user, c, http.StatusBadGateway, err.Error())
		return
	}
	http.Redirect(w, r, "/llm?notice=synced", http.StatusSeeOther)
}

func (s *Server) setModels(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLLM(w, r, user, c, http.StatusBadRequest, "Invalid form.")
		return
	}
	enabled := map[string]bool{}
	for _, id := range r.PostForm["enabled"] {
		enabled[id] = true
	}
	bulk := r.FormValue("bulk")
	var toggles []api.ModelToggle
	seen := map[string]bool{}
	for _, id := range r.PostForm["id"] {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		on := enabled[id]
		switch bulk {
		case "enable":
			on = true
		case "disable":
			on = false
		}
		toggles = append(toggles, api.ModelToggle{ID: id, Enabled: on})
	}
	if len(toggles) == 0 {
		s.renderLLM(w, r, user, c, http.StatusBadRequest, "No models to update. Sync the catalog first.")
		return
	}
	if err := c.AdminSetModels(r.Context(), toggles); err != nil {
		s.renderLLM(w, r, user, c, http.StatusBadGateway, err.Error())
		return
	}
	http.Redirect(w, r, "/llm?notice=models", http.StatusSeeOther)
}

func (s *Server) decideLLM(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLLM(w, r, user, c, http.StatusBadRequest, "Invalid form.")
		return
	}
	decision := r.FormValue("decision")
	if decision != "approve" && decision != "deny" {
		s.renderLLM(w, r, user, c, http.StatusBadRequest, "Choose approve or deny.")
		return
	}
	if _, err := c.AdminDecideLLM(r.Context(), r.PathValue("id"), decision == "approve", r.FormValue("note")); err != nil {
		s.renderLLM(w, r, user, c, http.StatusBadGateway, err.Error())
		return
	}
	http.Redirect(w, r, "/llm?notice=decided", http.StatusSeeOther)
}

func (s *Server) setCap(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderLLM(w, r, user, c, http.StatusBadRequest, "Invalid form.")
		return
	}
	userID := strings.TrimSpace(r.FormValue("user_id"))
	appID := strings.TrimSpace(r.FormValue("app_id"))
	if userID == "" && appID == "" {
		s.renderLLM(w, r, user, c, http.StatusBadRequest, "Enter a user id, an app id, or both.")
		return
	}
	raw := strings.TrimSpace(r.FormValue("spending_cap_usd"))
	cap, err := strconv.ParseFloat(raw, 64)
	if err != nil || cap < 0 {
		s.renderLLM(w, r, user, c, http.StatusBadRequest, "Spending cap must be a number zero or greater. Zero means no cap.")
		return
	}
	if err := c.AdminSetCap(r.Context(), api.SetCapRequest{
		UserID:         userID,
		AppID:          appID,
		SpendingCapUSD: &cap,
	}); err != nil {
		s.renderLLM(w, r, user, c, http.StatusBadGateway, err.Error())
		return
	}
	http.Redirect(w, r, "/llm?notice=cap", http.StatusSeeOther)
}

func (s *Server) oauth(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	s.renderOAuth(w, r, user, c, http.StatusOK, "", nil)
}

func (s *Server) renderOAuth(w http.ResponseWriter, r *http.Request, user api.User, c *apiclient.Client, status int, flash string, created *api.OAuthClient) {
	p := s.authed(user, "OAuth clients", "/oauth")
	applyNotice(r, &p)
	p.Flash = flash
	p.NewClient = created
	clients, err := c.AdminListOAuthClients(r.Context())
	if err != nil {
		if p.Flash != "" {
			p.Flash += " "
		}
		p.Flash += err.Error()
		if status == http.StatusOK {
			status = http.StatusBadGateway
		}
	}
	p.Clients = clients
	s.render(w, status, "oauth", p)
}

func (s *Server) createOAuth(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderOAuth(w, r, user, c, http.StatusBadRequest, "Invalid form.", nil)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.renderOAuth(w, r, user, c, http.StatusBadRequest, "Name is required.", nil)
		return
	}
	redirects := splitList(r.FormValue("redirect_uris"))
	if len(redirects) == 0 {
		s.renderOAuth(w, r, user, c, http.StatusBadRequest, "At least one redirect URI is required.", nil)
		return
	}
	scope := strings.TrimSpace(r.FormValue("scope"))
	if scope == "" {
		scope = "openid offline apps"
	}
	grants := r.PostForm["grant_type"]
	if len(grants) == 0 {
		grants = []string{"authorization_code", "refresh_token"}
	}
	created, err := c.AdminCreateOAuthClient(r.Context(), api.CreateOAuthClientRequest{
		Name:         name,
		GrantTypes:   grants,
		RedirectURIs: redirects,
		Scope:        scope,
	})
	if err != nil {
		s.renderOAuth(w, r, user, c, http.StatusBadGateway, err.Error(), nil)
		return
	}
	s.renderOAuth(w, r, user, c, http.StatusOK, "", &created)
}

func (s *Server) deleteOAuth(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := c.AdminDeleteOAuthClient(r.Context(), r.PathValue("id")); err != nil {
		s.renderOAuth(w, r, user, c, http.StatusBadGateway, err.Error(), nil)
		return
	}
	http.Redirect(w, r, "/oauth?notice=deleted", http.StatusSeeOther)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	p := s.authed(user, "Health", "/health")
	report, err := c.AdminHealth(r.Context())
	if err != nil {
		p.Flash = err.Error()
	}
	p.Health = report.Components
	name := "health"
	status := http.StatusOK
	if wantsFragment(r, "health") {
		name = "health-panel"
	} else if err != nil {
		status = http.StatusBadGateway
	}
	s.render(w, status, name, p)
}
