package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/app-plat/app-plat/pkg/api"
	"github.com/app-plat/app-plat/pkg/appyaml"
)

func cmdLogin(args []string, flagURL string, text bool) (string, error) {
	p, err := parseArgs(args, []string{"api-key", "api-url"}, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 0 {
		return "", fmt.Errorf("%s", usageLine("login"))
	}
	key := strings.TrimSpace(p.strings["api-key"])
	if key == "" {
		return "", fmt.Errorf("--api-key is required")
	}
	url := strings.TrimSpace(flagURL)
	if url == "" {
		url = strings.TrimSpace(p.strings["api-url"])
	}
	if url == "" {
		url = strings.TrimSpace(os.Getenv("APP_PLAT_API_URL"))
	}
	if url == "" {
		if existing, err := loadFile(); err == nil && existing.APIURL != "" {
			url = existing.APIURL
		}
	}
	if url == "" {
		url = defaultAPIURL
	}
	if err := saveFile(fileConfig{APIURL: url, APIKey: key}); err != nil {
		return "", err
	}
	return render(map[string]any{"ok": true, "api_url": strings.TrimRight(url, "/")}, text)
}

func cmdMe(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 0 {
		return "", fmt.Errorf("%s", usageLine("me"))
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	user, err := c.Me(ctx)
	if err != nil {
		return "", err
	}
	return render(user, text)
}

func cmdKeys(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("%s", usageLine("keys list"))
	}
	switch args[0] {
	case "create":
		return cmdKeysCreate(ctx, cfg, args[1:], text)
	case "list":
		return cmdKeysList(ctx, cfg, args[1:], text)
	case "revoke":
		return cmdKeysRevoke(ctx, cfg, args[1:], text)
	default:
		return "", fmt.Errorf("unknown command %q", "keys "+args[0])
	}
}

func cmdKeysCreate(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, []string{"name"}, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 0 {
		return "", fmt.Errorf("%s", usageLine("keys create"))
	}
	name := strings.TrimSpace(p.strings["name"])
	if name == "" {
		return "", fmt.Errorf("--name is required")
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	key, err := c.CreateAPIKey(ctx, name)
	if err != nil {
		return "", err
	}
	return render(key, text)
}

func cmdKeysList(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 0 {
		return "", fmt.Errorf("%s", usageLine("keys list"))
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	keys, err := c.ListAPIKeys(ctx)
	if err != nil {
		return "", err
	}
	if keys == nil {
		keys = []api.APIKey{}
	}
	return render(keys, text)
}

func cmdKeysRevoke(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 1 {
		return "", fmt.Errorf("%s", usageLine("keys revoke"))
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	if err := c.RevokeAPIKey(ctx, p.args[0]); err != nil {
		return "", err
	}
	return render(map[string]any{"ok": true, "id": p.args[0]}, text)
}

func cmdApps(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("%s", usageLine("apps list"))
	}
	switch args[0] {
	case "list":
		return cmdAppsList(ctx, cfg, args[1:], text)
	case "create":
		return cmdAppsCreate(ctx, cfg, args[1:], text)
	case "get":
		return cmdAppsGet(ctx, cfg, args[1:], text)
	case "upload":
		return cmdAppsUpload(ctx, cfg, args[1:], text)
	case "delete":
		return cmdAppsDelete(ctx, cfg, args[1:], text)
	case "env":
		return cmdAppsEnv(ctx, cfg, args[1:], text)
	default:
		return "", fmt.Errorf("unknown command %q", "apps "+args[0])
	}
}

func cmdAppsList(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 0 {
		return "", fmt.Errorf("%s", usageLine("apps list"))
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	apps, err := c.ListApps(ctx)
	if err != nil {
		return "", err
	}
	if apps == nil {
		apps = []api.App{}
	}
	return render(apps, text)
}

func cmdAppsCreate(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, []string{"name", "port", "persistence", "file"}, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 0 {
		return "", fmt.Errorf("%s", usageLine("apps create"))
	}
	var file *appyaml.File
	var rawYAML string
	if path := strings.TrimSpace(p.strings["file"]); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		parsed, err := appyaml.Parse(string(b))
		if err != nil {
			return "", err
		}
		file = &parsed
		rawYAML = string(b)
	}
	name := strings.TrimSpace(p.strings["name"])
	if name == "" && file != nil {
		name = strings.TrimSpace(file.Name)
	}
	if name == "" {
		return "", fmt.Errorf("--name is required")
	}
	port := 8080
	if raw, ok := p.strings["port"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid --port %q", raw)
		}
		port = n
	} else if file != nil && file.Port != 0 {
		port = file.Port
	}
	var pers []string
	if raw, ok := p.strings["persistence"]; ok {
		pers, err = appyaml.NormalizePersistence(splitCSV(raw))
		if err != nil {
			return "", err
		}
	} else if file != nil {
		pers = file.Persistence
	}
	if pers == nil {
		pers = []string{}
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	app, err := c.CreateApp(ctx, api.CreateAppRequest{
		Name:        name,
		Port:        port,
		Persistence: pers,
		AppYAML:     rawYAML,
	})
	if err != nil {
		return "", err
	}
	return render(app, text)
}

func cmdAppsGet(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 1 {
		return "", fmt.Errorf("%s", usageLine("apps get"))
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	app, err := resolveApp(ctx, c, p.args[0])
	if err != nil {
		return "", err
	}
	fresh, err := c.GetApp(ctx, app.ID)
	if err != nil {
		return "", err
	}
	return render(fresh, text)
}

func cmdAppsUpload(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 2 {
		return "", fmt.Errorf("%s", usageLine("apps upload"))
	}
	f, err := os.Open(p.args[1])
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("archive %q is a directory", p.args[1])
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	app, err := resolveApp(ctx, c, p.args[0])
	if err != nil {
		return "", err
	}
	out, err := c.UploadSource(ctx, app.ID, filepath.Base(p.args[1]), f)
	if err != nil {
		return "", err
	}
	return render(out, text)
}

func cmdAppsDelete(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 1 {
		return "", fmt.Errorf("%s", usageLine("apps delete"))
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	app, err := resolveApp(ctx, c, p.args[0])
	if err != nil {
		return "", err
	}
	if err := c.DeleteApp(ctx, app.ID); err != nil {
		return "", err
	}
	return render(map[string]any{"ok": true, "id": app.ID, "slug": app.Slug}, text)
}

func cmdAppsEnv(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("%s", usageLine("apps env list"))
	}
	switch args[0] {
	case "set":
		return cmdEnvSet(ctx, cfg, args[1:], text)
	case "list":
		return cmdEnvList(ctx, cfg, args[1:], text)
	default:
		return "", fmt.Errorf("unknown command %q", "apps env "+args[0])
	}
}

func cmdEnvSet(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, []string{"secret"})
	if err != nil {
		return "", err
	}
	if len(p.args) != 2 {
		return "", fmt.Errorf("%s", usageLine("apps env set"))
	}
	key, val, ok := strings.Cut(p.args[1], "=")
	if !ok || strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("expected KEY=VALUE")
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	app, err := resolveApp(ctx, c, p.args[0])
	if err != nil {
		return "", err
	}
	existing, err := c.GetEnv(ctx, app.ID)
	if err != nil {
		return "", err
	}
	updated := make([]api.EnvVar, 0, len(existing)+1)
	replaced := false
	for _, ev := range existing {
		if ev.Key == key {
			updated = append(updated, api.EnvVar{Key: key, Value: val, Secret: p.bools["secret"]})
			replaced = true
			continue
		}
		updated = append(updated, ev)
	}
	if !replaced {
		updated = append(updated, api.EnvVar{Key: key, Value: val, Secret: p.bools["secret"]})
	}
	if err := c.PutEnv(ctx, app.ID, updated); err != nil {
		return "", err
	}
	return render(map[string]any{"ok": true, "key": key, "secret": p.bools["secret"]}, text)
}

func cmdEnvList(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 1 {
		return "", fmt.Errorf("%s", usageLine("apps env list"))
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	app, err := resolveApp(ctx, c, p.args[0])
	if err != nil {
		return "", err
	}
	vars, err := c.GetEnv(ctx, app.ID)
	if err != nil {
		return "", err
	}
	if vars == nil {
		vars = []api.EnvVar{}
	}
	return render(vars, text)
}

func cmdLLM(ctx context.Context, cfg fileConfig, args []string, text bool) (string, error) {
	if len(args) == 0 || args[0] != "request" {
		return "", fmt.Errorf("%s", usageLine("llm request"))
	}
	p, err := parseArgs(args[1:], []string{"models", "cap"}, nil)
	if err != nil {
		return "", err
	}
	if len(p.args) != 1 {
		return "", fmt.Errorf("%s", usageLine("llm request"))
	}
	if _, ok := p.strings["models"]; !ok {
		return "", fmt.Errorf("--models is required")
	}
	models := splitCSV(p.strings["models"])
	if len(models) == 0 {
		return "", fmt.Errorf("--models is required")
	}
	rawCap, ok := p.strings["cap"]
	if !ok {
		return "", fmt.Errorf("--cap is required")
	}
	cap, err := strconv.ParseFloat(strings.TrimSpace(rawCap), 64)
	if err != nil {
		return "", fmt.Errorf("invalid --cap %q", rawCap)
	}
	if cap < 0 {
		return "", fmt.Errorf("--cap must be >= 0")
	}
	c, err := authed(cfg)
	if err != nil {
		return "", err
	}
	app, err := resolveApp(ctx, c, p.args[0])
	if err != nil {
		return "", err
	}
	req, err := c.CreateLLMRequest(ctx, api.CreateLLMRequest{
		AppID:          app.ID,
		Models:         models,
		SpendingCapUSD: cap,
	})
	if err != nil {
		return "", err
	}
	return render(req, text)
}

// resolveApp lists apps and matches id first, then slug.
func resolveApp(ctx context.Context, c interface {
	ListApps(context.Context) ([]api.App, error)
}, ref string) (api.App, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return api.App{}, fmt.Errorf("app slug or id is required")
	}
	apps, err := c.ListApps(ctx)
	if err != nil {
		return api.App{}, err
	}
	for _, app := range apps {
		if app.ID == ref {
			return app, nil
		}
	}
	var found *api.App
	for i := range apps {
		if apps[i].Slug == ref {
			if found != nil {
				return api.App{}, fmt.Errorf("multiple apps match slug %q", ref)
			}
			found = &apps[i]
		}
	}
	if found == nil {
		return api.App{}, fmt.Errorf("app %q not found", ref)
	}
	return *found, nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
