package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/config"
	"github.com/cropalato/promview/internal/httpapi"
	"github.com/cropalato/promview/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("promview stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return err
	}
	if len(os.Args) > 1 {
		if os.Args[1] != "migrate" {
			return errors.New("usage: promview [migrate]")
		}
		return postgres.ApplyMigrations(ctx, pool, cfg.MigrationsDir)
	}

	store := postgres.New(pool)
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           httpapi.New(cfg, store),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("promview listening", "address", cfg.ListenAddress)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
