// Command remedik is the remedik operator entrypoint.
//
// The current binary is a pre-MVP skeleton: it serves health/readiness
// probes and reports its version. The controller manager, alert gateway,
// and remediation engine land with the `add-mvp-core` OpenSpec change.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ratyx/remedik/internal/probes"
	"github.com/ratyx/remedik/internal/version"
)

func main() {
	var (
		probeAddr   = flag.String("probe-bind-address", ":8081", "address the health/readiness probe endpoint binds to")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger, *probeAddr); err != nil {
		logger.Error("remedik exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, probeAddr string) error {
	logger.Info("starting remedik", "version", version.String(), "probe_addr", probeAddr)

	srv := &http.Server{
		Addr:              probeAddr,
		Handler:           probes.NewMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("probe server: %w", err)
		}
		return nil
	}
}
