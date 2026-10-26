package userui

import (
	"net/http"
	"strings"

	"github.com/app-plat/app-plat/pkg/api"
)

func (s *Server) oauthLoginGet(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("login_challenge")
	p := page{
		Title:     "Sign in",
		Brand:     "user",
		Challenge: challenge,
		Next:      oauthLoginPath(challenge),
	}
	if challenge == "" {
		p.Flash = "Missing login challenge."
		s.render(w, http.StatusBadRequest, "oauth-login", p)
		return
	}
	user, ok := s.currentUser(w, r)
	if !ok {
		s.render(w, http.StatusOK, "oauth-login", p)
		return
	}
	p.LoggedIn = true
	p.User = &user
	p.Title = "Authorize"
	info, err := s.client(r).OAuthLoginInfo(r.Context(), challenge)
	if err != nil {
		p.Flash = err.Error()
		s.render(w, http.StatusBadGateway, "oauth-login", p)
		return
	}
	if info.Challenge != "" {
		p.Challenge = info.Challenge
	}
	p.ClientName = info.ClientName
	p.Scopes = info.RequestedScope
	p.Subject = info.Subject
	if info.Skip {
		redir, err := s.client(r).OAuthAcceptLogin(r.Context(), api.OAuthAcceptRequest{
			Challenge: p.Challenge,
			Remember:  true,
		})
		if err == nil && safeExternal(redir.RedirectTo) {
			http.Redirect(w, r, redir.RedirectTo, http.StatusFound)
			return
		}
		if err != nil {
			p.Flash = err.Error()
		} else {
			p.Flash = "Login accept did not return a redirect."
		}
	}
	s.render(w, http.StatusOK, "oauth-login", p)
}

func (s *Server) oauthLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "oauth-login", page{Title: "Authorize", Brand: "user", Flash: "Invalid form."})
		return
	}
	challenge := strings.TrimSpace(r.FormValue("login_challenge"))
	if challenge == "" {
		challenge = r.URL.Query().Get("login_challenge")
	}
	if _, ok := s.currentUser(w, r); !ok {
		http.Redirect(w, r, oauthLoginPath(challenge), http.StatusSeeOther)
		return
	}
	if challenge == "" {
		s.render(w, http.StatusBadRequest, "oauth-login", page{Title: "Authorize", Brand: "user", Flash: "Missing login challenge.", LoggedIn: true})
		return
	}
	redir, err := s.client(r).OAuthAcceptLogin(r.Context(), api.OAuthAcceptRequest{
		Challenge: challenge,
		Remember:  r.FormValue("remember") == "1",
	})
	if err != nil {
		s.render(w, http.StatusBadGateway, "oauth-login", page{
			Title: "Authorize", Brand: "user", Flash: err.Error(), LoggedIn: true, Challenge: challenge,
		})
		return
	}
	if !safeExternal(redir.RedirectTo) {
		s.render(w, http.StatusBadGateway, "oauth-login", page{
			Title: "Authorize", Brand: "user", Flash: "Login accept did not return a redirect.", LoggedIn: true, Challenge: challenge,
		})
		return
	}
	http.Redirect(w, r, redir.RedirectTo, http.StatusSeeOther)
}

func (s *Server) oauthConsentGet(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("consent_challenge")
	p := page{
		Title:     "Consent",
		Brand:     "user",
		Challenge: challenge,
		Next:      oauthConsentPath(challenge),
	}
	if challenge == "" {
		p.Flash = "Missing consent challenge."
		s.render(w, http.StatusBadRequest, "oauth-consent", p)
		return
	}
	user, ok := s.currentUser(w, r)
	if !ok {
		p.Title = "Sign in"
		s.render(w, http.StatusOK, "oauth-consent", p)
		return
	}
	p.LoggedIn = true
	p.User = &user
	info, err := s.client(r).OAuthConsentInfo(r.Context(), challenge)
	if err != nil {
		p.Flash = err.Error()
		s.render(w, http.StatusBadGateway, "oauth-consent", p)
		return
	}
	if info.Challenge != "" {
		p.Challenge = info.Challenge
	}
	p.ClientName = info.ClientName
	p.Scopes = info.RequestedScope
	p.Subject = info.Subject
	s.render(w, http.StatusOK, "oauth-consent", p)
}

func (s *Server) oauthConsentPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "oauth-consent", page{Title: "Consent", Brand: "user", Flash: "Invalid form."})
		return
	}
	challenge := strings.TrimSpace(r.FormValue("consent_challenge"))
	if challenge == "" {
		challenge = r.URL.Query().Get("consent_challenge")
	}
	user, ok := s.currentUser(w, r)
	if !ok {
		http.Redirect(w, r, oauthConsentPath(challenge), http.StatusSeeOther)
		return
	}
	p := page{
		Title: "Consent", Brand: "user", LoggedIn: true, User: &user, Challenge: challenge,
		Scopes: r.PostForm["scope"],
	}
	if challenge == "" {
		p.Flash = "Missing consent challenge."
		s.render(w, http.StatusBadRequest, "oauth-consent", p)
		return
	}
	scopes := make([]string, 0, len(r.PostForm["scope"]))
	seen := map[string]bool{}
	for _, scope := range r.PostForm["scope"] {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		scopes = append(scopes, scope)
	}
	if r.FormValue("scope_form") == "1" && len(scopes) == 0 {
		info, err := s.client(r).OAuthConsentInfo(r.Context(), challenge)
		if err == nil {
			p.ClientName = info.ClientName
			p.Scopes = info.RequestedScope
			p.Subject = info.Subject
		}
		p.Flash = "Select at least one scope."
		s.render(w, http.StatusBadRequest, "oauth-consent", p)
		return
	}
	redir, err := s.client(r).OAuthAcceptConsent(r.Context(), api.OAuthAcceptRequest{
		Challenge:  challenge,
		Remember:   r.FormValue("remember") == "1",
		GrantScope: scopes,
	})
	if err != nil {
		p.Flash = err.Error()
		s.render(w, http.StatusBadGateway, "oauth-consent", p)
		return
	}
	if !safeExternal(redir.RedirectTo) {
		p.Flash = "Consent accept did not return a redirect."
		s.render(w, http.StatusBadGateway, "oauth-consent", p)
		return
	}
	http.Redirect(w, r, redir.RedirectTo, http.StatusSeeOther)
}
