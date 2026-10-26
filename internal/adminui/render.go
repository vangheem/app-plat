package adminui

import (
	_ "embed"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/app-plat/app-plat/pkg/api"
)

//go:embed templates.html
var templatesHTML string

type navItem struct {
	Href  string
	Label string
}

type page struct {
	Title    string
	Active   string
	Brand    string
	Flash    string
	Notice   string
	User     *api.User
	Nav      []navItem
	Next     string
	Email    string
	Name     string
	FormRole string

	Apps         []api.App
	App          api.App
	Log          string
	Builds       []api.Build
	Users        []api.User
	Health       []api.ComponentHealth
	AppCount     int
	UserCount    int
	PendingCount int
	Pending      []api.LLMRequest
	Models       []api.LLMModel
	EnabledCount int
	Requests     []api.LLMRequest
	Usage        []api.UsageRow
	FilterUser   string
	FilterApp    string
	Clients      []api.OAuthClient
	NewClient    *api.OAuthClient
	CapUser      string
	CapApp       string
	CapAmount    string
}

func adminNav() []navItem {
	return []navItem{
		{Href: "/", Label: "Dashboard"},
		{Href: "/users", Label: "Users"},
		{Href: "/apps", Label: "Apps"},
		{Href: "/llm", Label: "LLM"},
		{Href: "/oauth", Label: "OAuth"},
		{Href: "/health", Label: "Health"},
	}
}

var templates = template.Must(template.New("adminui").Funcs(template.FuncMap{
	"badge":      badgeClass,
	"join":       strings.Join,
	"pathEscape": url.PathEscape,
}).Parse(templatesHTML))

func badgeClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "approved", "ok", "healthy", "up", "ready", "succeeded":
		return "badge badge-ok"
	case "failed", "denied", "deleting", "down", "error", "unhealthy":
		return "badge badge-bad"
	default:
		return "badge badge-warn"
	}
}

func (s *Server) authed(user api.User, title, active string) page {
	return page{
		Title:    title,
		Active:   active,
		Brand:    "admin",
		User:     &user,
		Nav:      adminNav(),
		FormRole: "user",
	}
}

func (s *Server) render(w http.ResponseWriter, status int, name string, p page) {
	if p.Brand == "" {
		p.Brand = "admin"
	}
	if p.FormRole == "" {
		p.FormRole = "user"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, name, p); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func noticeText(code string) string {
	switch code {
	case "created":
		return "User created."
	case "updated":
		return "Saved."
	case "synced":
		return "Model catalog synced."
	case "models":
		return "Model availability updated."
	case "decided":
		return "Request updated."
	case "cap":
		return "Spending cap updated."
	case "deleted":
		return "OAuth client deleted."
	default:
		return ""
	}
}

func applyNotice(r *http.Request, p *page) {
	p.Notice = noticeText(r.URL.Query().Get("notice"))
}

func wantsFragment(r *http.Request, name string) bool {
	return r.URL.Query().Get("fragment") == name
}

const sessionCookie = "app_plat_session"

func setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func safeNext(next string) bool {
	if next == "" || strings.ContainsAny(next, "\r\n\\") {
		return false
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return false
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" || u.Scheme != "" {
		return false
	}
	return true
}

func cleanNext(next string) string {
	if safeNext(next) {
		return next
	}
	return ""
}

func buildLog(app api.App, builds []api.Build) string {
	if len(builds) > 0 && builds[0].Log != "" {
		return builds[0].Log
	}
	if app.BuildLogTail != "" {
		return app.BuildLogTail
	}
	if len(builds) > 0 && builds[0].Error != "" {
		return builds[0].Error
	}
	return ""
}

func splitList(raw string) []string {
	raw = strings.ReplaceAll(raw, "\n", ",")
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func enabledCount(models []api.LLMModel) int {
	n := 0
	for _, m := range models {
		if m.Enabled {
			n++
		}
	}
	return n
}
