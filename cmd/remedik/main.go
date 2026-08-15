// Command remedik is the remedik operator entrypoint.
//
// The current binary is a pre-MVP build: it serves health probes and the
// Alertmanager webhook gateway. Alerts are decoded, validated and logged;
// the remediation engine that acts on them lands with the remaining tasks
// of the `add-mvp-core` OpenSpec change.
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
	"strings"
	"syscall"
	"time"

	"github.com/ratyx/remedik/internal/alert"
	"github.com/ratyx/remedik/internal/gateway"
	"github.com/ratyx/remedik/internal/probes"
	"github.com/ratyx/remedik/internal/version"
)

// shutdownTimeout bounds how long in-flight requests may take to drain.
const shutdownTimeout = 10 * time.Second

// tokenEnvVar holds the gateway bearer token. It is read from the
// environment rather than a flag so the value never appears in the process
// table; the chart mounts it from a Secret.
const tokenEnvVar = "REMEDIK_GATEWAY_TOKEN" //nolint:gosec // name of a variable, not a credential

func main() {
	var (
		probeAddr   = flag.String("probe-bind-address", ":8081", "address the health/readiness probes bind to")
		gatewayAddr = flag.String("gateway-bind-address", ":8090", "address the Alertmanager webhook gateway binds to")
		gatewayPath = flag.String("gateway-path", gateway.DefaultPath, "path the Alertmanager webhook is served on")
		logLevel    = flag.String("log-level", "info", "log level: debug, info, warn or error")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	level, err := parseLogLevel(*logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	if err := run(logger, *probeAddr, *gatewayAddr, *gatewayPath); err != nil {
		logger.Error("remedik exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, probeAddr, gatewayAddr, gatewayPath string) error {
	logger.Info("starting remedik",
		"version", version.String(),
		"probe_addr", probeAddr,
		"gateway_addr", gatewayAddr,
		"gateway_path", gatewayPath)

	handler, err := gateway.New(gateway.Config{
		Sink:   logSink(logger),
		Path:   gatewayPath,
		Token:  os.Getenv(tokenEnvVar),
		Logger: logger.With("component", "gateway"),
	})
	if err != nil {
		return fmt.Errorf("configure gateway: %w", err)
	}

	servers := []*http.Server{
		newServer(probeAddr, probes.NewMux()),
		newServer(gatewayAddr, handler.Mux()),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// One buffered slot per server, so a failing listener never blocks.
	errCh := make(chan error, len(servers))
	for _, srv := range servers {
		go func(s *http.Server) {
			if serveErr := s.ListenAndServe(); serveErr != nil &&
				!errors.Is(serveErr, http.ErrServerClosed) {
				errCh <- fmt.Errorf("listen on %s: %w", s.Addr, serveErr)
				return
			}
			errCh <- nil
		}(srv)
	}

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
		return shutdown(servers)
	case err := <-errCh:
		// Stop the remaining server before returning, so shutdown is
		// clean whether we are failing or exiting normally.
		if shutdownErr := shutdown(servers); shutdownErr != nil && err == nil {
			return shutdownErr
		}
		return err
	}
}

func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func shutdown(servers []*http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var errs []error
	for _, srv := range servers {
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown %s: %w", srv.Addr, err))
		}
	}
	return errors.Join(errs...)
}

// logSink reports the alerts remedik received. It is the placeholder for
// the remediation engine: until the engine exists, seeing exactly what was
// parsed is the useful behavior.
func logSink(logger *slog.Logger) gateway.Sink {
	log := logger.With("component", "sink")
	return gateway.SinkFunc(func(alerts []alert.Alert) {
		for _, a := range alerts {
			log.Info("alert received (no engine attached yet)",
				"alert", a.String(),
				"labels", a.Labels)
		}
	})
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid --log-level %q: want debug, info, warn or error", s)
	}
}
