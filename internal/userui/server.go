package userui

import (
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/app-plat/app-plat/pkg/api"
	"github.com/app-plat/app-plat/pkg/apiclient"
)

// Server is the HTMX user console.
type Server struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Server {
	return &Server{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

func ListenAndServe(addr, baseURL string) error {
	log.Printf("user ui listening on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           New(baseURL).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("/static/", staticHandler())
	mux.HandleFunc("GET /login", s.loginGet)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("GET /register", s.registerGet)
	mux.HandleFunc("POST /register", s.registerPost)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /apps/new", s.appNew)
	mux.HandleFunc("POST /apps", s.appCreate)
	mux.HandleFunc("GET /apps/{id}", s.appDetail)
	mux.HandleFunc("POST /apps/{id}/env", s.appEnv)
	mux.HandleFunc("POST /apps/{id}/source", s.appSource)
	mux.HandleFunc("POST /apps/{id}/rebuild", s.appRebuild)
	mux.HandleFunc("POST /apps/{id}/delete", s.appDelete)
	mux.HandleFunc("POST /apps/{id}/llm", s.appLLM)
	mux.HandleFunc("GET /settings", s.settings)
	mux.HandleFunc("POST /settings", s.createKey)
	mux.HandleFunc("POST /settings/{id}/revoke", s.revokeKey)
	mux.HandleFunc("GET /oauth2/login", s.oauthLoginGet)
	mux.HandleFunc("POST /oauth2/login", s.oauthLoginPost)
	mux.HandleFunc("GET /oauth2/consent", s.oauthConsentGet)
	mux.HandleFunc("POST /oauth2/consent", s.oauthConsentPost)
	return recoverMiddleware(mux)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic on %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) client(r *http.Request) *apiclient.Client {
	c := apiclient.New(s.baseURL)
	c.HTTP = s.http
	c.Token = sessionToken(r)
	return c
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) (api.User, bool) {
	c := s.client(r)
	if c.Token == "" {
		return api.User{}, false
	}
	user, err := c.Me(r.Context())
	if err != nil {
		clearSession(w)
		return api.User{}, false
	}
	return user, true
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (api.User, *apiclient.Client, bool) {
	user, ok := s.currentUser(w, r)
	if !ok {
		s.goLogin(w, r)
		return api.User{}, nil, false
	}
	return user, s.client(r), true
}

func (s *Server) goLogin(w http.ResponseWriter, r *http.Request) {
	dest := "/login"
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if next := r.URL.RequestURI(); safeNext(next) && next != "/" {
			dest = "/login?next=" + url.QueryEscape(next)
		}
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func (s *Server) loginGet(w http.ResponseWriter, r *http.Request) {
	next := cleanNext(r.URL.Query().Get("next"))
	if _, ok := s.currentUser(w, r); ok {
		dest := "/"
		if next != "" {
			dest = next
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}
	s.render(w, http.StatusOK, "login", page{Title: "Sign in", Next: next, Brand: "user"})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "login", page{Title: "Sign in", Flash: "Invalid form.", Brand: "user"})
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	next := cleanNext(r.FormValue("next"))
	p := page{Title: "Sign in", Email: email, Next: next, Brand: "user"}
	if email == "" || password == "" {
		p.Flash = "Email and password are required."
		s.render(w, http.StatusBadRequest, "login", p)
		return
	}
	auth, err := s.client(r).Login(r.Context(), email, password)
	if err != nil {
		p.Flash = err.Error()
		s.render(w, http.StatusUnauthorized, "login", p)
		return
	}
	if auth.SessionToken == "" {
		p.Flash = "Login did not return a session."
		s.render(w, http.StatusBadGateway, "login", p)
		return
	}
	setSession(w, auth.SessionToken)
	dest := "/"
	if next != "" {
		dest = next
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) registerGet(w http.ResponseWriter, r *http.Request) {
	next := cleanNext(r.URL.Query().Get("next"))
	if _, ok := s.currentUser(w, r); ok {
		dest := "/"
		if next != "" {
			dest = next
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}
	s.render(w, http.StatusOK, "register", page{Title: "Create account", Next: next, Brand: "user"})
}

func (s *Server) registerPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "register", page{Title: "Create account", Flash: "Invalid form.", Brand: "user"})
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")
	next := cleanNext(r.FormValue("next"))
	p := page{Title: "Create account", Email: email, Name: name, Next: next, Brand: "user"}
	if email == "" || password == "" {
		p.Flash = "Email and password are required."
		s.render(w, http.StatusBadRequest, "register", p)
		return
	}
	auth, err := s.client(r).Register(r.Context(), email, password, name)
	if err != nil {
		p.Flash = err.Error()
		s.render(w, http.StatusBadRequest, "register", p)
		return
	}
	if auth.SessionToken == "" {
		p.Flash = "Registration did not return a session."
		s.render(w, http.StatusBadGateway, "register", p)
		return
	}
	setSession(w, auth.SessionToken)
	dest := "/"
	if next != "" {
		dest = next
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c := s.client(r); c.Token != "" {
		_ = c.Logout(r.Context())
	}
	clearSession(w)
	dest := "/login"
	if next := cleanNext(r.FormValue("next")); next != "" {
		dest = next
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func cleanNext(next string) string {
	if safeNext(next) {
		return next
	}
	return ""
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	p := s.authed(user, "Apps", "/")
	applyNotice(r, &p)
	apps, err := c.ListApps(r.Context())
	if err != nil {
		p.Flash = err.Error()
		s.render(w, http.StatusBadGateway, "home", p)
		return
	}
	p.Apps = apps
	s.render(w, http.StatusOK, "home", p)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.renderSettings(w, r, user, c, http.StatusOK, "", "")
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, user api.User, c *apiclient.Client, status int, flash, newKey string) {
	p := s.authed(user, "API keys", "/settings")
	applyNotice(r, &p)
	p.Flash = flash
	p.NewKey = newKey
	keys, err := c.ListAPIKeys(r.Context())
	if err != nil {
		if p.Flash != "" {
			p.Flash += " "
		}
		p.Flash += err.Error()
		if status == http.StatusOK {
			status = http.StatusBadGateway
		}
	}
	p.Keys = keys
	s.render(w, status, "settings", p)
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderSettings(w, r, user, c, http.StatusBadRequest, "Invalid form.", "")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.renderSettings(w, r, user, c, http.StatusBadRequest, "Name is required.", "")
		return
	}
	key, err := c.CreateAPIKey(r.Context(), name)
	if err != nil {
		s.renderSettings(w, r, user, c, http.StatusBadGateway, err.Error(), "")
		return
	}
	s.renderSettings(w, r, user, c, http.StatusOK, "", key.Key)
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := c.RevokeAPIKey(r.Context(), r.PathValue("id")); err != nil {
		s.renderSettings(w, r, user, c, http.StatusBadGateway, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/settings?notice=revoked", http.StatusSeeOther)
}
