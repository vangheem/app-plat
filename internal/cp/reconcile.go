package cp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/app-plat/app-plat/internal/store"
	"github.com/app-plat/app-plat/pkg/api"
	"github.com/app-plat/app-plat/pkg/appyaml"
)

func (s *Server) Loop(ctx context.Context) {
	t := time.NewTicker(8 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcile(ctx)
		case <-s.wake:
			s.reconcile(ctx)
		}
	}
}

func (s *Server) reconcile(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.store.ListApps(ctx, "")
	if err != nil {
		s.log.Error("list apps", "err", err)
		return
	}
	for _, app := range apps {
		if err := s.reconcileOne(ctx, app); err != nil {
			s.log.Error("reconcile", "app", app.Slug, "err", err)
		}
	}
}

func (s *Server) reconcileOne(ctx context.Context, app store.App) error {
	fresh, err := s.store.GetApp(ctx, app.ID)
	if err != nil {
		return err
	}
	app = fresh
	if app.Status == "deleting" {
		if err := s.removeApp(ctx, app); err != nil {
			app.Error = err.Error()
			_ = s.store.UpdateApp(ctx, app)
			return err
		}
		return s.store.DeleteApp(ctx, app.ID)
	}
	if err := s.ensurePersistence(ctx, &app); err != nil {
		app.Status = "failed"
		app.Error = err.Error()
		_ = s.store.UpdateApp(ctx, app)
		return err
	}
	if app.Status == "building" {
		if err := s.buildApp(ctx, &app); err != nil {
			app.Status = "failed"
			app.Error = err.Error()
			_ = s.store.UpdateApp(ctx, app)
			_ = s.store.InsertBuild(ctx, store.Build{ID: newID("bld_"), AppID: app.ID, Status: "failed", Log: app.BuildLog, Error: err.Error()})
			return err
		}
		_ = s.store.InsertBuild(ctx, store.Build{ID: newID("bld_"), AppID: app.ID, Status: "succeeded", Image: app.Image, Log: app.BuildLog})
		app.Status = "deploying"
		if err := s.store.UpdateApp(ctx, app); err != nil {
			return err
		}
	}
	if app.Image == "" {
		return nil
	}
	if app.Status != "deploying" && app.Status != "running" && app.Status != "failed" {
		return nil
	}
	if err := s.applyApp(ctx, app); err != nil {
		app.Status = "failed"
		app.Error = err.Error()
		_ = s.store.UpdateApp(ctx, app)
		return err
	}
	if app.Status != "running" || app.Error != "" {
		app.Status = "running"
		app.Error = ""
		return s.store.UpdateApp(ctx, app)
	}
	return nil
}

func (s *Server) platformEnv(app store.App, res store.Resources) map[string]string {
	env := map[string]string{
		"PORT":                 itoa(app.Port),
		"APP_PLAT_PORT":        itoa(app.Port),
		"APP_PLAT_APP_NAME":    app.Slug,
		"APP_PLAT_PUBLIC_URL":  app.PublicURL,
	}
	if appyaml.Has(app.Persistence, "postgresql") && res.PGUser != "" {
		env["APP_PLAT_POSTGRES_HOST"] = s.cfg.PGAppHost
		env["APP_PLAT_POSTGRES_PORT"] = s.cfg.PGAppPort
		env["APP_PLAT_POSTGRES_USER"] = res.PGUser
		env["APP_PLAT_POSTGRES_PASSWORD"] = res.PGPassword
		env["APP_PLAT_POSTGRES_DATABASE"] = res.PGDatabase
		env["APP_PLAT_POSTGRES_SCHEMA"] = res.PGSchema
		env["APP_PLAT_POSTGRES_URL"] = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			url.QueryEscape(res.PGUser), url.QueryEscape(res.PGPassword), s.cfg.PGAppHost, s.cfg.PGAppPort, res.PGDatabase)
	}
	if appyaml.Has(app.Persistence, "blob") && res.BlobBucket != "" {
		env["APP_PLAT_BLOB_ENDPOINT"] = s.cfg.BlobPublicEndpoint
		env["APP_PLAT_BLOB_BUCKET"] = res.BlobBucket
		env["APP_PLAT_BLOB_ACCESS_KEY"] = res.BlobAccessKey
		env["APP_PLAT_BLOB_SECRET_KEY"] = res.BlobSecretKey
		env["APP_PLAT_BLOB_REGION"] = s.cfg.BlobRegion
		env["APP_PLAT_BLOB_USE_PATH_STYLE"] = "true"
	}
	if appyaml.Has(app.Persistence, "keyvalue") && res.KVUsername != "" {
		env["APP_PLAT_KV_HOST"] = hostOnly(s.cfg.KVAddr)
		env["APP_PLAT_KV_PORT"] = portOnly(s.cfg.KVAddr, "6379")
		env["APP_PLAT_KV_USERNAME"] = res.KVUsername
		env["APP_PLAT_KV_PASSWORD"] = res.KVPassword
		env["APP_PLAT_KV_PREFIX"] = res.KVPrefix
		env["APP_PLAT_KV_URL"] = fmt.Sprintf("redis://%s:%s@%s/0", url.QueryEscape(res.KVUsername), url.QueryEscape(res.KVPassword), s.cfg.KVAddr)
	}
	if res.LLMEnabled && res.LLMAPIKey != "" {
		env["APP_PLAT_LLM_BASE_URL"] = "http://llm-gateway:8083/v1"
		env["APP_PLAT_LLM_API_KEY"] = res.LLMAPIKey
	}
	return env
}

func hostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func portOnly(addr, fallback string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fallback
	}
	return port
}

func (s *Server) healthReport(ctx context.Context) api.HealthReport {
	checks := []struct {
		name string
		fn   func() error
	}{
		{"postgres", func() error { return s.store.Pool.Ping(ctx) }},
		{"kratos", func() error { return probe(ctx, s.cfg.KratosPublicURL+"/health/ready") }},
		{"hydra", func() error { return probe(ctx, s.cfg.HydraAdminURL+"/health/ready") }},
		{"registry", func() error { return probe(ctx, "http://"+s.cfg.Registry+"/v2/") }},
		{"blob", func() error { return probe(ctx, s.cfg.BlobEndpoint+"/minio/health/live") }},
	}
	out := api.HealthReport{Components: []api.ComponentHealth{}}
	for _, c := range checks {
		status, detail := "ok", ""
		if err := c.fn(); err != nil {
			status, detail = "down", err.Error()
		}
		out.Components = append(out.Components, api.ComponentHealth{Name: c.name, Status: status, Detail: detail})
	}
	return out
}

func probe(ctx context.Context, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}
