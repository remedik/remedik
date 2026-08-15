// Package probes provides the health and readiness HTTP endpoints served
// by the remedik binary.
package probes

import "net/http"

// NewMux returns an http.Handler serving GET /healthz and GET /readyz.
//
// Both endpoints report 200 while the process is up. Once the controller
// manager exists (OpenSpec change add-mvp-core), readiness will
// additionally reflect informer cache sync.
func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	mux.HandleFunc("GET /healthz", ok)
	mux.HandleFunc("GET /readyz", ok)
	return mux
}
