package adminui

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

// Server is the HTMX admin console.
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
	log.Printf("admin ui listening on %s", addr)
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
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /users", s.users)
	mux.HandleFunc("POST /users", s.createUser)
	mux.HandleFunc("POST /users/{id}", s.updateUser)
	mux.HandleFunc("GET /apps", s.apps)
	mux.HandleFunc("GET /apps/{id}", s.appDetail)
	mux.HandleFunc("GET /llm", s.llm)
	mux.HandleFunc("POST /llm/models/sync", s.syncModels)
	mux.HandleFunc("POST /llm/models", s.setModels)
	mux.HandleFunc("POST /llm/requests/{id}", s.decideLLM)
	mux.HandleFunc("POST /llm/caps", s.setCap)
	mux.HandleFunc("GET /oauth", s.oauth)
	mux.HandleFunc("POST /oauth", s.createOAuth)
	mux.HandleFunc("POST /oauth/{id}/delete", s.deleteOAuth)
	mux.HandleFunc("GET /health", s.health)
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

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (api.User, *apiclient.Client, bool) {
	user, c, ok := s.requireUser(w, r)
	if !ok {
		return user, c, false
	}
	if user.Role != "admin" {
		s.renderForbidden(w, user)
		return user, c, false
	}
	return user, c, true
}

func (s *Server) renderForbidden(w http.ResponseWriter, user api.User) {
	p := s.authed(user, "Forbidden", "")
	p.Nav = nil
	s.render(w, http.StatusForbidden, "forbidden", p)
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
	if user, ok := s.currentUser(w, r); ok {
		if user.Role != "admin" {
			s.renderForbidden(w, user)
			return
		}
		dest := "/"
		if next != "" {
			dest = next
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}
	s.render(w, http.StatusOK, "login", page{Title: "Sign in", Next: next, Brand: "admin"})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "login", page{Title: "Sign in", Flash: "Invalid form.", Brand: "admin"})
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	next := cleanNext(r.FormValue("next"))
	p := page{Title: "Sign in", Email: email, Next: next, Brand: "admin"}
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
	if auth.User.Role != "admin" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
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
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
