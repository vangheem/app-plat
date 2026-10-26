package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/app-plat/app-plat/internal/config"
	"github.com/app-plat/app-plat/internal/cp"
	"github.com/app-plat/app-plat/internal/store"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var st *store.Store
	var err error
	for i := 0; i < 60; i++ {
		st, err = store.Open(ctx, cfg.DatabaseURL)
		if err == nil {
			err = st.Migrate(ctx)
		}
		if err == nil {
			break
		}
		log.Info("waiting for database", "err", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := cp.New(cfg, st, log)
	go func() {
		if err := srv.Bootstrap(ctx); err != nil {
			log.Error("bootstrap admin", "err", err)
		}
	}()
	go srv.Loop(ctx)

	httpSrv := &http.Server{Addr: cfg.ListenAddr, Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	log.Info("control plane listening", "addr", cfg.ListenAddr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
}
