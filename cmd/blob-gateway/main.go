package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/app-plat/app-plat/internal/blobgw"
	"github.com/app-plat/app-plat/internal/config"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	addr := env("LISTEN_ADDR", ":9000")
	p := &blobgw.Proxy{
		ControlPlane: env("CONTROL_PLANE_URL", "http://control-plane:8080"),
		Token:        cfg.InternalToken,
		Upstream:     cfg.BlobEndpoint,
		AdminAccess:  cfg.BlobAccessKey,
		AdminSecret:  cfg.BlobSecretKey,
		Region:       cfg.BlobRegion,
		Log:          log,
	}
	log.Info("blob gateway listening", "addr", addr)
	if err := http.ListenAndServe(addr, p.Handler()); err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
