package userui

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
	Title   string
	Active  string
	Brand   string
	Flash   string
	Notice  string
	User    *api.User
	Nav     []navItem
	Next    string
	Email   string
	Name    string
	AppName string
	Port    string
	AppYAML string

	PersistPG   bool
	PersistBlob bool
	PersistKV   bool

	Apps    []api.App
	App     api.App
	EnvRows []api.EnvVar
	Log     string
	Builds  []api.Build
	Models  []api.LLMModel
	Policy  *api.LLMPolicy
	Grant   *api.LLMRequest
	Keys    []api.APIKey
	NewKey  string

	LoggedIn   bool
	Challenge  string
	ClientName string
	Scopes     []string
	Subject    string
}

func userNav() []navItem {
	return []navItem{
		{Href: "/", Label: "Apps"},
		{Href: "/apps/new", Label: "New app"},
		{Href: "/settings", Label: "Settings"},
	}
}

var templates = template.Must(template.New("userui").Funcs(template.FuncMap{
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
		Title:  title,
		Active: active,
		Brand:  "user",
		User:   &user,
		Nav:    userNav(),
	}
}

func (s *Server) render(w http.ResponseWriter, status int, name string, p page) {
	if p.Brand == "" {
		p.Brand = "user"
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
		return "App created. Upload a source archive to build it."
	case "saved":
		return "Environment saved."
	case "uploaded":
		return "Source uploaded. The build will start shortly."
	case "rebuilt":
		return "Rebuild started."
	case "deleted":
		return "App deleted."
	case "revoked":
		return "API key revoked."
	case "requested":
		return "LLM request submitted."
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

func safeExternal(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.IsAbs() {
		return u.Scheme == "http" || u.Scheme == "https"
	}
	return strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//")
}

func appPath(id string) string {
	return "/apps/" + url.PathEscape(id)
}

func oauthLoginPath(challenge string) string {
	return "/oauth2/login?" + url.Values{"login_challenge": {challenge}}.Encode()
}

func oauthConsentPath(challenge string) string {
	return "/oauth2/consent?" + url.Values{"consent_challenge": {challenge}}.Encode()
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

func latestGrant(reqs []api.LLMRequest, appID string) (api.LLMRequest, bool) {
	var best api.LLMRequest
	found := false
	for _, req := range reqs {
		if req.AppID != appID {
			continue
		}
		if !found || req.CreatedAt > best.CreatedAt {
			best = req
			found = true
		}
	}
	return best, found
}

const extraEnvRows = 3

func displayEnv(vars []api.EnvVar) []api.EnvVar {
	rows := make([]api.EnvVar, 0, len(vars)+extraEnvRows)
	for _, v := range vars {
		if strings.TrimSpace(v.Key) == "" {
			continue
		}
		if v.Secret {
			v.Value = ""
		}
		rows = append(rows, v)
	}
	for i := 0; i < extraEnvRows; i++ {
		rows = append(rows, api.EnvVar{})
	}
	return rows
}

func envFromForm(r *http.Request) []api.EnvVar {
	keys := r.PostForm["key"]
	vals := r.PostForm["value"]
	out := make([]api.EnvVar, 0, len(keys))
	for i, key := range keys {
		val := ""
		if i < len(vals) {
			val = vals[i]
		}
		out = append(out, api.EnvVar{
			Key:    key,
			Value:  val,
			Secret: r.PostForm.Get("secret_"+itoa(i)) == "1",
		})
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
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

// mergeEnv keeps a stored secret when the form submits it blank.
// GetEnv masks those values as empty with Secret=true; copying the previous
// EnvVar preserves the key, and the control plane restores the stored value.
func mergeEnv(existing, submitted []api.EnvVar) ([]api.EnvVar, error) {
	prev := make(map[string]api.EnvVar, len(existing))
	for _, v := range existing {
		prev[v.Key] = v
	}
	out := make([]api.EnvVar, 0, len(submitted))
	seen := map[string]bool{}
	for _, v := range submitted {
		v.Key = strings.TrimSpace(v.Key)
		if v.Key == "" || seen[v.Key] {
			continue
		}
		if !envNameOK(v.Key) {
			return nil, errInvalidEnv(v.Key)
		}
		seen[v.Key] = true
		if v.Secret && v.Value == "" {
			if old, ok := prev[v.Key]; ok {
				v.Value = old.Value
				v.Secret = true
			}
		}
		out = append(out, v)
	}
	return out, nil
}

type envNameError string

func (e envNameError) Error() string { return string(e) }

func errInvalidEnv(key string) error {
	return envNameError("invalid env name " + key)
}
