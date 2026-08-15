// Command remedik is the remedik operator.
//
// One process runs three things: the Alertmanager webhook gateway, the
// controller that drives each Remediation through its lifecycle, and the
// health and metrics endpoints. They share a manager, so caches, graceful
// shutdown and metrics registration happen once.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/ratyx/remedik/api/v1alpha1"
	"github.com/ratyx/remedik/internal/action"
	"github.com/ratyx/remedik/internal/action/workload"
	"github.com/ratyx/remedik/internal/engine"
	"github.com/ratyx/remedik/internal/gateway"
	"github.com/ratyx/remedik/internal/guards"
	"github.com/ratyx/remedik/internal/metrics"
	"github.com/ratyx/remedik/internal/version"
)

const (
	// tokenEnvVar holds the gateway bearer token. It comes from the
	// environment rather than a flag so the value never appears in the
	// process table; the chart mounts it from a Secret.
	tokenEnvVar = "REMEDIK_GATEWAY_TOKEN" //nolint:gosec // the name of a variable, not a credential

	// namespaceEnvVar is the namespace Remediation resources are created
	// in. The chart sets it from the pod's own namespace.
	namespaceEnvVar = "REMEDIK_NAMESPACE"

	// historyPruneInterval is how often the in-memory guard history drops
	// records older than its retention.
	historyPruneInterval = 5 * time.Minute
)

type options struct {
	metricsAddr  string
	probeAddr    string
	gatewayAddr  string
	gatewayPath  string
	namespace    string
	dryRun       bool
	historyLimit int
	logLevel     string
	showVersion  bool
}

func main() {
	opts := parseFlags()

	if opts.showVersion {
		fmt.Println(version.String())
		return
	}

	level, err := parseLogLevel(opts.logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	// controller-runtime logs through logr; bridging keeps one log stream
	// and one format for everything the operator emits.
	ctrl.SetLogger(logr.FromSlogHandler(handler))

	if err := run(logger, opts); err != nil {
		logger.Error("remedik exited with error", "err", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var opts options

	flag.StringVar(&opts.metricsAddr, "metrics-bind-address", ":8080",
		"address the Prometheus metrics endpoint binds to")
	flag.StringVar(&opts.probeAddr, "probe-bind-address", ":8081",
		"address the health and readiness probes bind to")
	flag.StringVar(&opts.gatewayAddr, "gateway-bind-address", ":8090",
		"address the Alertmanager webhook gateway binds to")
	flag.StringVar(&opts.gatewayPath, "gateway-path", gateway.DefaultPath,
		"path the Alertmanager webhook is served on")
	flag.StringVar(&opts.namespace, "namespace", os.Getenv(namespaceEnvVar),
		"namespace Remediation resources are created in")
	flag.BoolVar(&opts.dryRun, "dry-run", true,
		"evaluate and record what would happen without changing anything")
	flag.IntVar(&opts.historyLimit, "history-limit", engine.DefaultHistoryLimit,
		"terminal Remediation resources kept per strategy")
	flag.StringVar(&opts.logLevel, "log-level", "info",
		"log level: debug, info, warn or error")
	flag.BoolVar(&opts.showVersion, "version", false, "print version and exit")
	flag.Parse()

	return opts
}

func run(logger *slog.Logger, opts options) error {
	if opts.namespace == "" {
		return fmt.Errorf("no namespace: set --namespace or %s", namespaceEnvVar)
	}

	logger.Info("starting remedik",
		"version", version.String(),
		"namespace", opts.namespace,
		"dry_run", opts.dryRun,
		"gateway_addr", opts.gatewayAddr,
		"gateway_path", opts.gatewayPath)

	if opts.dryRun {
		logger.Warn("dry-run is on: remediations are recorded as Simulated and nothing is changed. " +
			"Set dryRun=false in the chart values once the reports look right.")
	}

	scheme, err := buildScheme()
	if err != nil {
		return err
	}

	metrics.MustRegister()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: opts.metricsAddr},
		HealthProbeBindAddress: opts.probeAddr,
		// Single replica by design in this version: the state machine
		// treats a Remediation found Running as interrupted, which is only
		// sound while one process reconciles it. Leader election arrives
		// with multi-replica support.
		LeaderElection: false,
	})
	if err != nil {
		return fmt.Errorf("create manager: %w", err)
	}

	registry, err := action.NewRegistry(
		workload.NewDeploymentRestart(mgr.GetClient(), time.Now),
	)
	if err != nil {
		return fmt.Errorf("build action registry: %w", err)
	}
	logger.Info("actions registered", "actions", registry.Names())

	history := guards.NewMemoryHistory(0)

	reconciler := &engine.RemediationReconciler{
		Client:       mgr.GetClient(),
		Registry:     registry,
		History:      history,
		DryRun:       opts.dryRun,
		HistoryLimit: opts.historyLimit,
		Metrics:      metrics.Engine{},
		Logger:       logger.With("component", "reconciler"),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("register the remediation controller: %w", err)
	}

	// Guards live in memory; without replaying what is already in the
	// cluster, a restart would forget every cooldown and hourly count.
	//
	// This runs before the manager starts, deliberately: the guards must be
	// warm before the gateway can accept its first alert, and a runnable
	// added to the manager would race the listener.
	loadCtx, cancelLoad := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelLoad()
	loader := &engine.HistoryLoader{
		Reader:    mgr.GetAPIReader(),
		History:   history,
		Namespace: opts.namespace,
		Logger:    logger.With("component", "history"),
	}
	if err := loader.Load(loadCtx); err != nil {
		return fmt.Errorf("rebuild guard history: %w", err)
	}

	sink := &engine.Sink{
		Client:    mgr.GetClient(),
		Registry:  registry,
		History:   history,
		Namespace: opts.namespace,
		DryRun:    opts.dryRun,
		Metrics:   metrics.Engine{},
		Logger:    logger.With("component", "sink"),
	}

	handler, err := gateway.New(gateway.Config{
		Sink:    sink,
		Path:    opts.gatewayPath,
		Token:   os.Getenv(tokenEnvVar),
		Metrics: metrics.Gateway{},
		Logger:  logger.With("component", "gateway"),
	})
	if err != nil {
		return fmt.Errorf("configure gateway: %w", err)
	}

	if err := mgr.Add(&gatewayServer{
		server: &http.Server{
			Addr:              opts.gatewayAddr,
			Handler:           handler.Mux(),
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger: logger.With("component", "gateway"),
	}); err != nil {
		return fmt.Errorf("register the gateway server: %w", err)
	}

	if err := mgr.Add(&historyPruner{history: history, every: historyPruneInterval}); err != nil {
		return fmt.Errorf("register the history pruner: %w", err)
	}

	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("register the health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("register the readiness check: %w", err)
	}

	logger.Info("remedik is running")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("run manager: %w", err)
	}
	return nil
}

func buildScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register built-in types: %w", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register remedik types: %w", err)
	}
	return scheme, nil
}

// gatewayServer runs the webhook listener under the manager, so it shares
// the manager's lifecycle and shuts down with everything else.
type gatewayServer struct {
	server *http.Server
	logger *slog.Logger
}

// Start implements manager.Runnable.
func (g *gatewayServer) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		g.logger.Info("gateway listening", "addr", g.server.Addr)
		if err := g.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen on %s: %w", g.server.Addr, err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return g.server.Shutdown(shutdownCtx)
	}
}

// historyPruner keeps the in-memory guard history bounded. Pruning is
// explicit rather than driven by record timestamps, because replaying old
// Remediation resources must not look like time has passed.
type historyPruner struct {
	history *guards.MemoryHistory
	every   time.Duration
}

// Start implements manager.Runnable.
func (p *historyPruner) Start(ctx context.Context) error {
	ticker := time.NewTicker(p.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			p.history.Prune(now)
		}
	}
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
