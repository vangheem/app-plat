package userui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/app-plat/app-plat/pkg/api"
	"github.com/app-plat/app-plat/pkg/apiclient"
)

func (s *Server) appNew(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := s.authed(user, "New app", "/apps/new")
	p.Port = "8080"
	s.render(w, http.StatusOK, "app-new", p)
}

func (s *Server) appCreate(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		p := s.authed(user, "New app", "/apps/new")
		p.Flash = "Invalid form."
		p.Port = "8080"
		s.render(w, http.StatusBadRequest, "app-new", p)
		return
	}
	p := s.authed(user, "New app", "/apps/new")
	p.AppName = strings.TrimSpace(r.FormValue("name"))
	p.Port = strings.TrimSpace(r.FormValue("port"))
	p.AppYAML = r.FormValue("app_yaml")
	p.PersistPG = formHas(r, "persistence", "postgresql")
	p.PersistBlob = formHas(r, "persistence", "blob")
	p.PersistKV = formHas(r, "persistence", "keyvalue")
	port := 0
	if p.Port != "" {
		n, err := strconv.Atoi(p.Port)
		if err != nil || n < 1 || n > 65535 {
			p.Flash = "Port must be between 1 and 65535."
			s.render(w, http.StatusBadRequest, "app-new", p)
			return
		}
		port = n
	}
	if p.AppName == "" && strings.TrimSpace(p.AppYAML) == "" {
		p.Flash = "Name is required."
		s.render(w, http.StatusBadRequest, "app-new", p)
		return
	}
	req := api.CreateAppRequest{
		Name:        p.AppName,
		Port:        port,
		Persistence: selectedPersistence(r),
		AppYAML:     p.AppYAML,
	}
	app, err := c.CreateApp(r.Context(), req)
	if err != nil {
		p.Flash = err.Error()
		s.render(w, http.StatusBadGateway, "app-new", p)
		return
	}
	http.Redirect(w, r, appPath(app.ID)+"?notice=created", http.StatusSeeOther)
}

func formHas(r *http.Request, name, want string) bool {
	for _, v := range r.Form[name] {
		if v == want {
			return true
		}
	}
	return false
}

func selectedPersistence(r *http.Request) []string {
	out := make([]string, 0, 3)
	for _, name := range []string{"postgresql", "blob", "keyvalue"} {
		if formHas(r, "persistence", name) {
			out = append(out, name)
		}
	}
	return out
}

func (s *Server) appDetail(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if wantsFragment(r, "status") {
		s.renderStatusFragment(w, r, c)
		return
	}
	s.renderAppDetail(w, r, user, c, http.StatusOK, "", nil)
}

func (s *Server) renderStatusFragment(w http.ResponseWriter, r *http.Request, c *apiclient.Client) {
	id := r.PathValue("id")
	app, err := c.GetApp(r.Context(), id)
	if err != nil {
		s.render(w, http.StatusOK, "status-panel", page{
			App: api.App{ID: id, Status: "error", Error: err.Error()},
		})
		return
	}
	builds, _ := c.ListBuilds(r.Context(), app.ID)
	s.render(w, http.StatusOK, "status-panel", page{
		App:    app,
		Log:    buildLog(app, builds),
		Builds: builds,
	})
}

func (s *Server) renderAppDetail(w http.ResponseWriter, r *http.Request, user api.User, c *apiclient.Client, status int, flash string, envOverride []api.EnvVar) {
	id := r.PathValue("id")
	app, err := c.GetApp(r.Context(), id)
	if err != nil {
		p := s.authed(user, "App", "/")
		p.Flash = err.Error()
		code := http.StatusBadGateway
		switch err.Error() {
		case "app not found":
			code = http.StatusNotFound
			p.Title = "App not found"
		case "not your app":
			code = http.StatusForbidden
			p.Title = "Forbidden"
		}
		s.render(w, code, "message", p)
		return
	}
	var flashes []string
	if flash != "" {
		flashes = append(flashes, flash)
	}
	builds, err := c.ListBuilds(r.Context(), app.ID)
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	var rows []api.EnvVar
	if envOverride != nil {
		rows = envOverride
	} else {
		rows, err = c.GetEnv(r.Context(), app.ID)
		if err != nil {
			flashes = append(flashes, err.Error())
		}
	}
	models, err := c.ListLLMModels(r.Context())
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	reqs, err := c.ListLLMRequests(r.Context())
	if err != nil {
		flashes = append(flashes, err.Error())
	}
	policy, err := c.LLMPolicy(r.Context())
	var policyPtr *api.LLMPolicy
	if err != nil {
		flashes = append(flashes, err.Error())
	} else {
		policyPtr = &policy
	}
	grant, has := latestGrant(reqs, app.ID)
	var grantPtr *api.LLMRequest
	if has {
		grantPtr = &grant
	}
	title := app.Name
	if title == "" {
		title = "App"
	}
	p := s.authed(user, title, "/")
	p.Flash = strings.Join(flashes, " ")
	applyNotice(r, &p)
	p.App = app
	p.EnvRows = displayEnv(rows)
	p.Log = buildLog(app, builds)
	p.Builds = builds
	p.Models = models
	p.Policy = policyPtr
	p.Grant = grantPtr
	if status == 0 {
		status = http.StatusOK
	}
	s.render(w, status, "app-detail", p)
}

func (s *Server) appEnv(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadRequest, "Invalid form.", nil)
		return
	}
	submitted := envFromForm(r)
	existing, err := c.GetEnv(r.Context(), r.PathValue("id"))
	if err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadGateway, err.Error(), submitted)
		return
	}
	vars, err := mergeEnv(existing, submitted)
	if err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadRequest, err.Error(), submitted)
		return
	}
	if err := c.PutEnv(r.Context(), r.PathValue("id"), vars); err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadGateway, err.Error(), submitted)
		return
	}
	http.Redirect(w, r, appPath(r.PathValue("id"))+"?notice=saved", http.StatusSeeOther)
}

func (s *Server) appSource(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadRequest, "Could not read archive: "+err.Error(), nil)
		return
	}
	file, hdr, err := r.FormFile("archive")
	if err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadRequest, "Archive file is required.", nil)
		return
	}
	defer file.Close()
	filename := "archive.tar.gz"
	if hdr != nil && hdr.Filename != "" {
		filename = hdr.Filename
	}
	if _, err := c.UploadSource(r.Context(), r.PathValue("id"), filename, file); err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadGateway, err.Error(), nil)
		return
	}
	http.Redirect(w, r, appPath(r.PathValue("id"))+"?notice=uploaded", http.StatusSeeOther)
}

func (s *Server) appRebuild(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if _, err := c.RebuildApp(r.Context(), r.PathValue("id")); err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadGateway, err.Error(), nil)
		return
	}
	http.Redirect(w, r, appPath(r.PathValue("id"))+"?notice=rebuilt", http.StatusSeeOther)
}

func (s *Server) appDelete(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := c.DeleteApp(r.Context(), r.PathValue("id")); err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadGateway, err.Error(), nil)
		return
	}
	http.Redirect(w, r, "/?notice=deleted", http.StatusSeeOther)
}

func (s *Server) appLLM(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadRequest, "Invalid form.", nil)
		return
	}
	models := make([]string, 0, len(r.PostForm["models"]))
	seen := map[string]bool{}
	for _, m := range r.PostForm["models"] {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		models = append(models, m)
	}
	if len(models) == 0 {
		s.renderAppDetail(w, r, user, c, http.StatusBadRequest, "Select at least one model.", nil)
		return
	}
	rawCap := strings.TrimSpace(r.FormValue("spending_cap_usd"))
	cap, err := strconv.ParseFloat(rawCap, 64)
	if err != nil || cap < 0 {
		s.renderAppDetail(w, r, user, c, http.StatusBadRequest, "Spending cap must be a number zero or greater. Zero means no cap.", nil)
		return
	}
	if _, err := c.CreateLLMRequest(r.Context(), api.CreateLLMRequest{
		AppID:          r.PathValue("id"),
		Models:         models,
		SpendingCapUSD: cap,
	}); err != nil {
		s.renderAppDetail(w, r, user, c, http.StatusBadGateway, err.Error(), nil)
		return
	}
	http.Redirect(w, r, appPath(r.PathValue("id"))+"?notice=requested", http.StatusSeeOther)
}
