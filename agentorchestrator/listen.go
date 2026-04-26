package agentorchestrator

import (
	"context"
	"net/http"
)

// ServerPair holds both the HTTP server and the orchestrator for lifecycle management.
type ServerPair struct {
	HTTP     *http.Server
	internal *Server
}

// Shutdown performs graceful shutdown: stops HTTP, then drains the executor.
func (sp *ServerPair) Shutdown(ctx context.Context) error {
	// Stop accepting new HTTP connections.
	httpErr := sp.HTTP.Shutdown(ctx)
	// Drain in-flight executor jobs.
	if sp.internal != nil && sp.internal.executor != nil {
		sp.internal.executor.Shutdown(ctx)
	}
	return httpErr
}

// NewServer creates the HTTP server without starting it.
func NewServer(addr, version string, opts ...ServerOption) (*ServerPair, error) {
	s, err := New(version, opts...)
	if err != nil {
		return nil, err
	}
	return &ServerPair{
		HTTP: &http.Server{
			Addr:    addr,
			Handler: s.Handler(),
		},
		internal: s,
	}, nil
}

// ListenAndServe creates and starts the server (kept for backward compat).
func ListenAndServe(addr, version string, opts ...ServerOption) error {
	sp, err := NewServer(addr, version, opts...)
	if err != nil {
		return err
	}
	return sp.HTTP.ListenAndServe()
}
