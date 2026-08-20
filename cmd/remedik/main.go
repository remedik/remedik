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
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
	"github.com/remedik/remedik/internal/action/external"
	"github.com/remedik/remedik/internal/action/node"
	"github.com/remedik/remedik/internal/action/workload"
	"github.com/remedik/remedik/internal/dashboard"
	"github.com/remedik/remedik/internal/engine"
	"github.com/remedik/remedik/internal/gateway"
	"github.com/remedik/remedik/internal/guards"
	"github.com/remedik/remedik/internal/metrics"
	"github.com/remedik/remedik/internal/version"
)

const (
	// tokenEnvVar holds the gateway bearer token. It comes from the
	// environment rather than a flag so the value never appears in the
	// process table; the chart mounts it from a Secret.
	tokenEnvVar = "REMEDIK_GATEWAY_TOKEN" //nolint:gosec // the name of a variable, not a credential

	// dashboardTokenEnvVar holds the dashboard token, for the same reason.
	dashboardTokenEnvVar = "REMEDIK_DASHBOARD_TOKEN" //nolint:gosec // the name of a variable, not a credential

	// namespaceEnvVar is the namespace Remediation resources are created
	// in. The chart sets it from the pod's own namespace.
	namespaceEnvVar = "REMEDIK_NAMESPACE"

	// clusterEnvVar names the cluster, for the dashboard's header.
	clusterEnvVar = "REMEDIK_CLUSTER"

	// serviceAccountEnvVar is remedik's own ServiceAccount, which a
	// remediation Job is refused if it asks to run as. The chart sets it
	// from the pod's own spec.
	serviceAccountEnvVar = "REMEDIK_SERVICE_ACCOUNT"

	// historyPruneInterval is how often the in-memory guard history drops
	// records older than its retention.
	historyPruneInterval = 5 * time.Minute
)

type options struct {
	metricsAddr    string
	probeAddr      string
	gatewayAddr    string
	gatewayPath    string
	dashboardAddr  string
	actions        []string
	serviceAccount string
	namespace      string
	cluster        string
	dryRun         bool
	posture        []string
	historyLimit   int
	concurrency    int
	pauseConfigMap string
	maxRecordAge   time.Duration
	logLevel       string
	showVersion    bool
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
	// The dashboard is off unless an address is given. One flag rather than
	// an enable flag and an address: "not listening anywhere" is the
	// clearest possible spelling of "not enabled", and it cannot be set to
	// a contradictory pair.
	flag.StringVar(&opts.dashboardAddr, "dashboard-bind-address", "",
		"address the read-only web dashboard binds to; empty disables it (for example "+
			dashboard.DefaultBindAddress+")")
	var actions string
	flag.StringVar(&actions, "actions", "",
		"comma-separated actions to enable; empty enables every action this build implements. "+
			"The chart passes exactly the actions it granted RBAC for, so a strategy naming a "+
			"disabled action is reported as not ready rather than failing mid-incident")
	flag.StringVar(&opts.namespace, "namespace", os.Getenv(namespaceEnvVar),
		"namespace Remediation resources are created in")
	flag.StringVar(&opts.serviceAccount, "service-account", os.Getenv(serviceAccountEnvVar),
		"remedik's own ServiceAccount. Remediation Jobs are refused if they ask to run as it, "+
			"so that a strategy author cannot inherit the operator's permissions by writing one word")
	flag.StringVar(&opts.cluster, "cluster-name", os.Getenv(clusterEnvVar),
		"a name for the cluster this operator watches, shown in the dashboard. "+
			"Purely a label: remedik sees one cluster because it runs in one")
	flag.BoolVar(&opts.dryRun, "dry-run", true,
		"the default posture: evaluate and record what would happen without changing anything")
	flag.Func("namespace-posture",
		"override the default posture for one namespace, as \"namespace=live\" or "+
			"\"namespace=dryRun\". Repeatable. This is how a cluster runs live where "+
			"remediation has been earned and reporting-only everywhere else",
		func(value string) error {
			opts.posture = append(opts.posture, value)
			return nil
		})
	flag.IntVar(&opts.historyLimit, "history-limit", engine.DefaultHistoryLimit,
		"terminal Remediation resources kept per strategy")
	flag.StringVar(&opts.pauseConfigMap, "pause-configmap", "remedik-pause",
		"ConfigMap in the operator's namespace whose `paused` key stops remediation")
	flag.IntVar(&opts.concurrency, "concurrency", engine.DefaultConcurrency,
		"how many remediations may be changing the cluster at once")
	flag.DurationVar(&opts.maxRecordAge, "max-record-age", 0,
		"delete terminal Remediation records older than this; zero keeps them by count only")
	flag.StringVar(&opts.logLevel, "log-level", "info",
		"log level: debug, info, warn or error")
	flag.BoolVar(&opts.showVersion, "version", false, "print version and exit")
	flag.Parse()

	opts.actions = splitActions(actions)
	return opts
}

func run(logger *slog.Logger, opts options) error {
	if opts.namespace == "" {
		return fmt.Errorf("no namespace: set --namespace or %s", namespaceEnvVar)
	}

	overrides, err := engine.ParsePosture(opts.posture)
	if err != nil {
		return fmt.Errorf("--namespace-posture: %w", err)
	}
	posture := engine.NewPosture(opts.dryRun, overrides)

	logger.Info("starting remedik",
		"version", version.String(),
		"namespace", opts.namespace,
		"posture", posture.String(),
		"gateway_addr", opts.gatewayAddr,
		"gateway_path", opts.gatewayPath,
		"dashboard_addr", dashboardStatus(opts.dashboardAddr))

	switch {
	case posture.Mixed():
		// The warning that matters most here is the one nobody would think
		// to ask for: an operator reading "dryRun: true" in the values file
		// and believing nothing acts.
		logger.Warn("the posture is mixed, so the default does not describe the whole cluster",
			"acts_in", posture.Namespaces(engine.ModeLive),
			"reports_only_in", posture.Namespaces(engine.ModeDryRun),
			"default", string(posture.Default))
	case opts.dryRun:
		logger.Warn("dry-run is on: remediations are recorded as Simulated and nothing is changed. " +
			"Set dryRun=false in the chart values, or make one namespace live with " +
			"namespacePosture, once the reports look right.")
	}

	scheme, err := buildScheme()
	if err != nil {
		return err
	}

	metrics.MustRegister()

	// Established before anything is built, because the gateway consults it:
	// the moment a SIGTERM arrives this process stops accepting alerts, even
	// though it keeps its lease and keeps working until the runnables finish.
	signalCtx := ctrl.SetupSignalHandler()

	restConfig := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: opts.metricsAddr},
		HealthProbeBindAddress: opts.probeAddr,
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				// Remediation records only ever live in the operator's own
				// namespace. Watching them cluster-wide — the default —
				// would demand cluster-wide permission on them for no
				// benefit, so the cache is scoped to match the RBAC the
				// chart grants.
				&v1alpha1.Remediation{}: {
					Namespaces: map[string]cache.Config{opts.namespace: {}},
				},
			},
		},
		// The state machine treats a Remediation found Running as
		// interrupted, which is only sound while one process reconciles it —
		// and the guards hold their state in memory, so two instances would
		// each enforce a cooldown the other cannot see. A lease makes that
		// design correct rather than merely assumed: `kubectl scale
		// --replicas=2` is now failover, not double remediation.
		LeaderElection:             true,
		LeaderElectionID:           "remedik.remedik.dev",
		LeaderElectionNamespace:    opts.namespace,
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
		// The gateway keeps listening on every replica and answers 503 when
		// it is not the leader, so releasing on shutdown makes the handover
		// quick rather than waiting out the lease.
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return fmt.Errorf("create manager: %w", err)
	}

	// Actions read and write arbitrary workloads across the cluster. They
	// use a direct client rather than the manager's cached one: caching
	// every Deployment in the cluster to touch a handful would cost memory
	// for nothing, and it would force the chart to grant list and watch on
	// Deployments where get and patch are enough.
	directClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create direct client: %w", err)
	}

	// A clientset alongside the controller-runtime client: pod logs are a
	// subresource the latter does not model, and the tail of what a
	// remediation Job printed is most of that action's value.
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}

	registry, err := buildRegistry(registryDeps{
		client:         directClient,
		logs:           external.NewPodLogs(clientset),
		namespace:      opts.namespace,
		serviceAccount: opts.serviceAccount,
	}, opts.actions)
	if err != nil {
		return fmt.Errorf("build action registry: %w", err)
	}
	logger.Info("actions registered", "actions", registry.Names())

	// The posture metrics read through the manager's cache, so a scrape
	// costs no API call. They are registered here, after the manager exists,
	// because that is where the cache is.
	metrics.MustRegisterPosture(metrics.PostureConfig{
		Version:          version.String(),
		DryRun:           opts.dryRun,
		NamespacePosture: postureLabels(posture),
		Snapshot: postureFrom(&engine.Snapshotter{
			Reader:    mgr.GetCache(),
			Namespace: opts.namespace,
		}),
		Logger: logger.With("component", "metrics"),
	})

	history := guards.NewMemoryHistory(0)

	reconciler := &engine.RemediationReconciler{
		Client:       mgr.GetClient(),
		Registry:     registry,
		History:      history,
		HistoryLimit: opts.historyLimit,
		Concurrency:  opts.concurrency,
		Metrics:      metrics.Engine{},
		Events:       mgr.GetEventRecorder("remedik"),
		Mapper:       mgr.GetRESTMapper(),
		Logger:       logger.With("component", "reconciler"),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("register the remediation controller: %w", err)
	}

	// The second controller writes nothing but status, and it is what makes a
	// strategy answer back: applied at 10:00, it says within seconds whether
	// remedik could run it, instead of leaving that to be found out at 03:00.
	strategies := &engine.StrategyReconciler{
		Client:    mgr.GetClient(),
		Registry:  registry,
		Namespace: opts.namespace,
		Logger:    logger.With("component", "strategies"),
	}
	if err := strategies.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("register the strategy controller: %w", err)
	}

	// Guards live in memory; without replaying what is already in the
	// cluster, a remediation cooled down a minute ago would run again.
	//
	// The replay is tied to winning the lease, not to the process starting.
	// A standby replica that loaded at boot and took over six hours later
	// would be enforcing six-hour-old cooldowns — which is exactly the
	// mistake leader election is supposed to prevent, arriving through the
	// side door.
	warm := make(chan struct{})
	warmer := &guardWarmer{
		loader: &engine.HistoryLoader{
			Reader:    mgr.GetAPIReader(),
			History:   history,
			Namespace: opts.namespace,
			Logger:    logger.With("component", "history"),
		},
		ready:  warm,
		logger: logger.With("component", "history"),
	}
	if err := mgr.Add(warmer); err != nil {
		return fmt.Errorf("register the guard warmer: %w", err)
	}

	// The kill switch, read from a ConfigMap every few seconds.
	//
	// A chart value is how you configure a cluster; this is how you stop it at
	// three in the morning, with no restart and no rollout. It does not silence
	// remedik -- it forces dry-run everywhere, so the record of what would have
	// happened survives the decision to stop it.
	pause := &engine.Pause{}
	if err := mgr.Add(&engine.PauseWatcher{
		// The uncached reader: the manager's cache is not started until the
		// manager is, and this has to answer before the first alert.
		Reader:    mgr.GetAPIReader(),
		Namespace: opts.namespace,
		Name:      opts.pauseConfigMap,
		Pause:     pause,
		Logger:    logger.With("component", "pause"),
	}); err != nil {
		return fmt.Errorf("register the pause watcher: %w", err)
	}

	sink := &engine.Sink{
		Client:    mgr.GetClient(),
		Registry:  registry,
		History:   history,
		Workloads: &engine.WorkloadHealth{Reader: directClient},
		Namespace: opts.namespace,
		Posture:   posture,
		Metrics:   metrics.Engine{},
		Events:    mgr.GetEventRecorder("remedik"),
		Logger:    logger.With("component", "sink"),
		Pause:     pause,
	}

	// The gateway accepts once the guards are warm — which happens only
	// after this instance holds the lease — and stops the moment shutdown
	// begins.
	//
	// The second half matters more than it looks. During a rolling update
	// the old pod is still a Service endpoint and still holds the lease, so
	// an alert can land on a process that is about to be killed and have its
	// remediation cut in half. The record is honest about it — Running can
	// only mean the process died, so it is failed as Interrupted — but
	// losing a remediation on every upgrade is not a good trade. Refusing
	// with 503 while the replacement is already serving costs the sender one
	// retry.
	handler, err := gateway.New(gateway.Config{
		Sink:    sink,
		Path:    opts.gatewayPath,
		Token:   os.Getenv(tokenEnvVar),
		Metrics: metrics.Gateway{},
		Logger:  logger.With("component", "gateway"),
		Accepting: func() bool {
			select {
			case <-signalCtx.Done():
				return false
			default:
			}
			select {
			case <-warm:
				return true
			default:
				return false
			}
		},
	})
	if err != nil {
		return fmt.Errorf("configure gateway: %w", err)
	}

	if err := mgr.Add(newHTTPServer("gateway", opts.gatewayAddr, handler.Mux(), logger)); err != nil {
		return fmt.Errorf("register the gateway server: %w", err)
	}

	// The dashboard reads through the manager's cache, so it lists exactly
	// what the reconciler already watches: serving a page costs no API call
	// and needs no permission the operator does not already hold.
	//
	// mgr.GetClient() is passed as a dashboard.Reader, an interface with no
	// write method on it. The handler cannot mutate anything because there
	// is nothing on its client to call.
	if opts.dashboardAddr != "" {
		ui, err := dashboard.New(dashboard.Config{
			Reader:    mgr.GetClient(),
			Namespace: opts.namespace,
			Token:     os.Getenv(dashboardTokenEnvVar),
			Posture:   dashboardPosture(posture),
			// Read per request, not captured: the switch is flipped at
			// runtime, and a dashboard that needed a restart to notice would
			// be the one place an operator checks after stopping remediation
			// and the one place still claiming it is running.
			Paused:  func() (bool, string) { return pause.Paused(), pause.Reason() },
			Cluster: opts.cluster,
			Version: version.String(),
			Logger:  logger.With("component", "dashboard"),
		})
		if err != nil {
			return fmt.Errorf("configure dashboard: %w", err)
		}
		if err := mgr.Add(newHTTPServer("dashboard", opts.dashboardAddr, ui.Mux(), logger)); err != nil {
			return fmt.Errorf("register the dashboard server: %w", err)
		}
	}

	if err := mgr.Add(&historyPruner{history: history, every: historyPruneInterval}); err != nil {
		return fmt.Errorf("register the history pruner: %w", err)
	}

	// Retention on a schedule, not only when a remediation completes.
	//
	// Pruning inside the terminal write only ever reclaimed records for the
	// strategy that had just finished one, so a strategy that was disabled,
	// renamed, deleted or had merely gone quiet kept everything it ever made.
	// Over the life of a cluster that is a leak rather than a policy.
	if err := mgr.Add(&engine.Sweeper{
		Client:          mgr.GetClient(),
		Namespace:       opts.namespace,
		MaxAge:          opts.maxRecordAge,
		KeepPerStrategy: opts.historyLimit,
		Metrics:         metrics.Engine{},
		Logger:          logger.With("component", "retention"),
	}); err != nil {
		return fmt.Errorf("register the retention sweeper: %w", err)
	}

	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("register the health check: %w", err)
	}
	// Readiness is deliberately not leadership.
	//
	// Gating it on "would this instance accept an alert" was tried and
	// reverted: a standby then never becomes ready, so `helm --wait` and
	// `kubectl rollout status` never finish on a deployment with more than
	// one replica, and the failover this change exists to allow cannot be
	// installed with ordinary tooling.
	//
	// A standby is ready because it is doing its job — waiting, and answering
	// 503 with Retry-After so a sender retries onto the leader. That is the
	// contract stated above, and readiness is not where it is enforced.
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("register the readiness check: %w", err)
	}

	logger.Info("remedik is running")
	if err := mgr.Start(signalCtx); err != nil {
		return fmt.Errorf("run manager: %w", err)
	}
	return nil
}

// postureFrom adapts the engine's snapshot to the metrics package's.
//
// The two structs are deliberately identical, so this is a conversion rather
// than a translation: the engine owns what it can report, and the metrics
// package owns how it is published, and neither has to import the other.
func postureFrom(s *engine.Snapshotter) metrics.SnapshotFunc {
	return func(ctx context.Context) (metrics.Snapshot, error) {
		snapshot, err := s.Snapshot(ctx)
		return metrics.Snapshot(snapshot), err
	}
}

// postureLabels flattens the overrides for the namespace_posture metric.
func postureLabels(p engine.Posture) map[string]string {
	if len(p.Overrides) == 0 {
		return nil
	}
	labels := make(map[string]string, len(p.Overrides))
	for namespace, mode := range p.Overrides {
		labels[namespace] = string(mode)
	}
	return labels
}

// dashboardPosture converts the engine's posture into the shape the pages
// render, the same way postureFrom converts a snapshot for the metrics: the
// engine says what is true, and the adapters translate.
func dashboardPosture(p engine.Posture) dashboard.Posture {
	return dashboard.Posture{
		DryRun:     p.Default != engine.ModeLive,
		Live:       p.Namespaces(engine.ModeLive),
		DryRunOnly: p.Namespaces(engine.ModeDryRun),
	}
}

// buildRegistry registers the actions this operator is configured to run.
//
// An action absent from the registry is not merely unusable: a strategy
// naming it is reported as not ready when it is applied, rather than
// failing during the incident it was written for. That is why the chart
// passes exactly the actions it granted permissions for — the two lists
// disagreeing is a misconfiguration worth finding on a Tuesday.
type registryDeps struct {
	client         client.Client
	logs           *external.PodLogs
	namespace      string
	serviceAccount string
}

func buildRegistry(deps registryDeps, enabled []string) (*action.Registry, error) {
	c := deps.client
	available := []action.Action{
		workload.NewDeploymentRestart(c, time.Now),
		workload.NewWorkloadRestart(c, time.Now),
		workload.NewPodDelete(c),
		workload.NewJobDelete(c),
		workload.NewDeploymentRollback(c),
		workload.NewDeploymentScale(c),
		workload.NewHPAScale(c),
		external.NewWebhookCall(c, deps.namespace),
		external.NewJobRun(c, deps.logs, deps.serviceAccount, time.Now),
		external.NewScriptRun(c, deps.logs, deps.serviceAccount, time.Now),
		node.NewCordon(c),
		node.NewUncordon(c),
		node.NewDrain(c),
		node.NewPVCExpand(c),
	}

	if len(enabled) == 0 {
		return action.NewRegistry(available...)
	}

	byName := make(map[string]action.Action, len(available))
	names := make([]string, 0, len(available))
	for _, a := range available {
		byName[a.Name()] = a
		names = append(names, a.Name())
	}

	selected := make([]action.Action, 0, len(enabled))
	for _, name := range enabled {
		a, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("--actions names %q, which this build does not implement (available: %s)",
				name, strings.Join(names, ", "))
		}
		selected = append(selected, a)
	}
	return action.NewRegistry(selected...)
}

// splitActions parses the --actions list, ignoring the empty entries a
// templated flag tends to produce.
func splitActions(raw string) []string {
	var out []string
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
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

// httpServer runs one listener under the manager, so it shares the
// manager's lifecycle and shuts down with everything else. The gateway and
// the dashboard each get one; they listen on separate ports so a cluster's
// owner can apply different network policy to each.
//
// The manager starts runnables after its caches have synced, which is what
// makes it safe for these handlers to read through the cached client.
type httpServer struct {
	name   string
	server *http.Server
	logger *slog.Logger
}

func newHTTPServer(name, addr string, handler http.Handler, logger *slog.Logger) *httpServer {
	return &httpServer{
		name: name,
		server: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger: logger.With("component", name),
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
//
// False, and this is load-bearing. controller-runtime starts a runnable that
// says nothing only after the lease is won, so without this the gateway,
// the dashboard and the metrics endpoint would not exist on a standby at
// all — the connection would be refused rather than answered with 503, and
// that is precisely the outcome the design rejects: a Service has one set of
// endpoints, so a replica with no listener is indistinguishable from remedik
// being down.
//
// Every server here listens on every replica. Which of them accepts alerts
// is the gateway's own decision, made per request, and it is the only place
// leadership belongs.
func (s *httpServer) NeedLeaderElection() bool { return false }

// Start implements manager.Runnable.
func (s *httpServer) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info(s.name+" listening", "addr", s.server.Addr)
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen on %s: %w", s.server.Addr, err)
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
		return s.server.Shutdown(shutdownCtx)
	}
}

// guardWarmer replays the guard history when this instance becomes the
// leader.
//
// It needs the lease, so a standby does no work and — more to the point —
// does not hold state that will be stale by the time it is used. The gateway
// waits on its ready channel, so no alert is accepted before the cooldowns
// that should stop it are known.
type guardWarmer struct {
	loader *engine.HistoryLoader
	ready  chan struct{}
	logger *slog.Logger
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
func (g *guardWarmer) NeedLeaderElection() bool { return true }

// Start implements manager.Runnable.
func (g *guardWarmer) Start(ctx context.Context) error {
	loadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := g.loader.Load(loadCtx); err != nil {
		// Returning stops the manager, which is right: a leader that cannot
		// rebuild its guards would remediate without them.
		return fmt.Errorf("rebuild guard history: %w", err)
	}

	g.logger.Info("guards are warm; the gateway is accepting alerts")
	close(g.ready)

	<-ctx.Done()
	return nil
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

// dashboardStatus describes the dashboard for the startup log line, where
// an empty address would read as a configuration the operator forgot rather
// than the default it is.
func dashboardStatus(addr string) string {
	if addr == "" {
		return "disabled"
	}
	return addr
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
