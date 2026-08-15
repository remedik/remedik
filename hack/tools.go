//go:build tools

// Package tools pins the dependencies remedik's Kubernetes integration
// needs, before the code that imports them is written.
//
// This is the standard "tools.go" pattern: the build tag keeps these
// imports out of every normal build, while `go mod tidy` and
// `go mod vendor` still see them. That is what makes it possible to
// resolve and vendor the operator's dependency tree as one deliberate
// step, rather than discovering it package by package.
//
// Remove an entry once nothing outside this file needs it.
package tools

import (
	// Controller runtime: manager, client, reconcile loop, events.
	_ "sigs.k8s.io/controller-runtime"
	_ "sigs.k8s.io/controller-runtime/pkg/builder"
	_ "sigs.k8s.io/controller-runtime/pkg/client"
	_ "sigs.k8s.io/controller-runtime/pkg/client/config"
	_ "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	_ "sigs.k8s.io/controller-runtime/pkg/envtest"
	_ "sigs.k8s.io/controller-runtime/pkg/healthz"
	_ "sigs.k8s.io/controller-runtime/pkg/log"
	_ "sigs.k8s.io/controller-runtime/pkg/log/zap"
	_ "sigs.k8s.io/controller-runtime/pkg/manager"
	_ "sigs.k8s.io/controller-runtime/pkg/metrics"
	_ "sigs.k8s.io/controller-runtime/pkg/reconcile"

	// Kubernetes API types the actions operate on.
	_ "k8s.io/api/apps/v1"
	_ "k8s.io/api/core/v1"
	_ "k8s.io/apimachinery/pkg/api/errors"
	_ "k8s.io/apimachinery/pkg/types"
	_ "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/tools/record"

	// Metrics: the Prometheus adapter behind the Recorder interfaces.
	_ "github.com/prometheus/client_golang/prometheus"
)
