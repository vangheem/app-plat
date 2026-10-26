package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/app-plat/app-plat/pkg/api"
)

type fakeAPI struct {
	srv        *httptest.Server
	mu         sync.Mutex
	putEnv     []api.EnvVar
	llm        api.CreateLLMRequest
	create     api.CreateAppRequest
	uploadName string
	uploadBody string
	getPath    string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	mux := http.NewServeMux()
	okAuth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer apk_test" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	app := api.App{ID: "app_1", Name: "Demo", Slug: "demo", Port: 8080, Status: "running", Persistence: []string{"postgresql"}}
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(api.User{ID: "u1", Email: "a@b.c", Name: "Ada", Role: "user"})
	})
	mux.HandleFunc("GET /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]api.App{app})
	})
	mux.HandleFunc("POST /api/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		var req api.CreateAppRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		f.mu.Lock()
		f.create = req
		f.mu.Unlock()
		created := app
		created.Name = req.Name
		created.Port = req.Port
		created.Persistence = req.Persistence
		_ = json.NewEncoder(w).Encode(created)
	})
	mux.HandleFunc("GET /api/v1/apps/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		f.mu.Lock()
		f.getPath = r.URL.Path
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(app)
	})
	mux.HandleFunc("DELETE /api/v1/apps/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/apps/{id}/source", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		file, hdr, err := r.FormFile("archive")
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		defer file.Close()
		b, _ := io.ReadAll(file)
		f.mu.Lock()
		f.uploadName = hdr.Filename
		f.uploadBody = string(b)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(app)
	})
	mux.HandleFunc("GET /api/v1/apps/{id}/env", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]api.EnvVar{
			{Key: "KEEP", Value: "1", Secret: false},
			{Key: "SECRET", Value: "", Secret: true},
		})
	})
	mux.HandleFunc("PUT /api/v1/apps/{id}/env", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		var req api.PutEnvRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		f.mu.Lock()
		f.putEnv = req.Vars
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/llm/requests", func(w http.ResponseWriter, r *http.Request) {
		if !okAuth(w, r) {
			return
		}
		var req api.CreateLLMRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		f.mu.Lock()
		f.llm = req
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(api.LLMRequest{
			ID: "lr1", AppID: req.AppID, Status: "approved", Models: req.Models, SpendingCapUSD: req.SpendingCapUSD,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func useHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APP_PLAT_API_URL", "")
	t.Setenv("APP_PLAT_API_KEY", "")
}

func TestLoginAndCommands(t *testing.T) {
	useHome(t)
	apiSrv := newFakeAPI(t)

	out, err := Execute(t.Context(), []string{"login", "--api-key", "apk_test", "--api-url", apiSrv.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, apiSrv.srv.URL) {
		t.Fatalf("login output %s", out)
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %o", st.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved fileConfig
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.APIKey != "apk_test" || saved.APIURL != apiSrv.srv.URL {
		t.Fatalf("saved %#v", saved)
	}

	me, err := Execute(t.Context(), []string{"me"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(me, "a@b.c") || !strings.HasPrefix(strings.TrimSpace(me), "{") {
		t.Fatalf("me %s", me)
	}
	text, err := Execute(t.Context(), []string{"--text", "me"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") || !strings.Contains(text, "email:") {
		t.Fatalf("text me %s", text)
	}

	got, err := Execute(t.Context(), []string{"apps", "get", "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "app_1") {
		t.Fatalf("get %s", got)
	}
	apiSrv.mu.Lock()
	if apiSrv.getPath != "/api/v1/apps/app_1" {
		t.Fatalf("resolved path %s", apiSrv.getPath)
	}
	apiSrv.mu.Unlock()

	yamlPath := filepath.Join(t.TempDir(), "app.yaml")
	yamlRaw := "name: From File\nport: 9090\npersistence:\n  - postgres\n  - kv\n"
	if err := os.WriteFile(yamlPath, []byte(yamlRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(t.Context(), []string{"apps", "create", "--name", "From Flag", "--file", yamlPath}); err != nil {
		t.Fatal(err)
	}
	apiSrv.mu.Lock()
	if apiSrv.create.Name != "From Flag" || apiSrv.create.Port != 9090 || apiSrv.create.AppYAML != yamlRaw {
		t.Fatalf("create %#v", apiSrv.create)
	}
	if strings.Join(apiSrv.create.Persistence, ",") != "postgresql,keyvalue" {
		t.Fatalf("persistence %#v", apiSrv.create.Persistence)
	}
	apiSrv.mu.Unlock()

	archive := filepath.Join(t.TempDir(), "src.tar.gz")
	if err := os.WriteFile(archive, []byte("archive-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(t.Context(), []string{"apps", "upload", "demo", archive}); err != nil {
		t.Fatal(err)
	}
	apiSrv.mu.Lock()
	if apiSrv.uploadName != "src.tar.gz" || apiSrv.uploadBody != "archive-bytes" {
		t.Fatalf("upload %s %s", apiSrv.uploadName, apiSrv.uploadBody)
	}
	apiSrv.mu.Unlock()

	if _, err := Execute(t.Context(), []string{"apps", "env", "set", "demo", "NEW=value", "--secret"}); err != nil {
		t.Fatal(err)
	}
	apiSrv.mu.Lock()
	foundNew := false
	foundKeep := false
	foundSecret := false
	for _, ev := range apiSrv.putEnv {
		switch ev.Key {
		case "NEW":
			foundNew = ev.Value == "value" && ev.Secret
		case "KEEP":
			foundKeep = ev.Value == "1" && !ev.Secret
		case "SECRET":
			foundSecret = ev.Secret && ev.Value == ""
		}
	}
	apiSrv.mu.Unlock()
	if !foundNew || !foundKeep || !foundSecret {
		t.Fatalf("env merge %#v", apiSrv.putEnv)
	}

	llmOut, err := Execute(t.Context(), []string{"llm", "request", "demo", "--models", "a, b", "--cap", "5"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llmOut, "approved") {
		t.Fatalf("llm %s", llmOut)
	}
	apiSrv.mu.Lock()
	if apiSrv.llm.AppID != "app_1" || apiSrv.llm.SpendingCapUSD != 5 || strings.Join(apiSrv.llm.Models, ",") != "a,b" {
		t.Fatalf("llm req %#v", apiSrv.llm)
	}
	apiSrv.mu.Unlock()

	if _, err := Execute(t.Context(), []string{"mcp"}); err == nil {
		t.Fatal("expected mcp rejection")
	}
	if _, err := Execute(t.Context(), []string{"nope"}); err == nil {
		t.Fatal("expected unknown command error")
	}
}

func TestEnvOverridesConfig(t *testing.T) {
	useHome(t)
	apiSrv := newFakeAPI(t)
	if _, err := Execute(t.Context(), []string{"login", "--api-key", "apk_old", "--api-url", "http://127.0.0.1:9"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_PLAT_API_URL", apiSrv.srv.URL)
	t.Setenv("APP_PLAT_API_KEY", "apk_test")
	out, err := Execute(t.Context(), []string{"me"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a@b.c") {
		t.Fatalf("me %s", out)
	}
}

func TestCatalogFilter(t *testing.T) {
	text := CatalogText("revoke")
	if !strings.Contains(text, "keys revoke") || strings.Contains(text, "apps list") {
		t.Fatalf("filter %q", text)
	}
	if !strings.Contains(CatalogText(""), "llm request") || !strings.Contains(CatalogText(""), "mcp") {
		t.Fatal(CatalogText(""))
	}
}
