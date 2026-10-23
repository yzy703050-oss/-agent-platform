package main

import (
	"agent-platform/backend/internal/bootstrap"
	"agent-platform/backend/internal/config"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server_stopped", "error", err.Error())
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	app, err := bootstrap.New(startup, cfg)
	cancel()
	if err != nil {
		return err
	}
	defer app.Close()
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: app.Handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	failed := make(chan error, 1)
	go func() { failed <- server.ListenAndServe() }()
	slog.Info("server_started", "address", cfg.HTTPAddr)
	select {
	case err = <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP server failed")
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
