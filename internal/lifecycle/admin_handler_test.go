package lifecycle

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminPurgeHandler_MethodNotAllowed(t *testing.T) {
	handler := AdminPurgeHandler(nil, PurgeConfig{})

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/purge", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// Note: Testing with a real database requires integration test setup.
// The handler delegates to RunPurgeNow which is tested via config and
// integration tests separately.
