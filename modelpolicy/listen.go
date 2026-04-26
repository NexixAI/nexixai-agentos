package modelpolicy

import "net/http"

// NewServer creates the HTTP server without starting it.
func NewServer(addr, version string) *http.Server {
	s := New(version)
	return &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}
}

// ListenAndServe creates and starts the server (kept for backward compat).
func ListenAndServe(addr, version string) error {
	return NewServer(addr, version).ListenAndServe()
}
