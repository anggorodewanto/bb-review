// Command bb-review serves the bb-review web UI and its Bitbucket proxy API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/config"
	"github.com/anggorodewanto/bb-review/internal/scanner"
	"github.com/anggorodewanto/bb-review/internal/server"
	"github.com/anggorodewanto/bb-review/internal/store"
	"github.com/anggorodewanto/bb-review/web"
)

const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	configPath := flag.String("config", config.DefaultPath(), "path to config.json")
	listen := flag.String("listen", "", "listen address (overrides config listen)")
	flag.Parse()

	cfg, err := config.Load(*configPath, logger)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}

	localStore, err := store.Open(cfg.StateFile())
	if err != nil {
		return fmt.Errorf("open state file: %w", err)
	}

	assets, err := web.Dist()
	if err != nil {
		return fmt.Errorf("open embedded frontend: %w", err)
	}
	indexAvailable := true
	if _, statErr := fs.Stat(assets, "index.html"); statErr != nil {
		indexAvailable = false
		logger.Warn("frontend not built; serving placeholder page. Run `make build`", "dist", "web/dist")
	}

	client := bitbucket.New(bitbucket.Options{
		BaseURL: cfg.BaseURL,
		Email:   cfg.Email,
		Token:   cfg.Token,
		Logger:  logger,
	})

	scanner := scanner.New(scanner.Options{
		Client:            client,
		Store:             localStore,
		Logger:            logger,
		Workspace:         cfg.Workspace,
		ConfigRepos:       cfg.RepoRefs(),
		ActiveDays:        cfg.ActiveDaysOrDefault(),
		ScanInterval:      cfg.ScanIntervalOrDefault(),
		FullSweepInterval: cfg.FullSweepIntervalOrDefault(),
	})

	handler := server.New(server.Options{
		Config:  cfg,
		Client:  client,
		Store:   localStore,
		Logger:  logger,
		Assets:  assets,
		Scanner: scanner,
	}).Handler()

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go scanner.Run(ctx)

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening",
			"addr", cfg.Listen,
			"workspace", cfg.Workspace,
			"repos", len(cfg.Repos),
			"scan_interval", cfg.ScanIntervalOrDefault().String(),
			"full_sweep_interval", cfg.FullSweepIntervalOrDefault().String(),
			"state", localStore.Path(),
			"frontend_built", indexAvailable,
		)
		serveErr <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
