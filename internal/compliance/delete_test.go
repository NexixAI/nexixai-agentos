package compliance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeleteHandler_MissingConfirmHeader(t *testing.T) {
	h := NewDeleteHandler(nil, nil)

	r := newAuthRequest(http.MethodPost, "/v1/tenants/data-delete", "tnt_test", "owner")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["code"] != "confirmation_required" {
		t.Errorf("error code = %q, want confirmation_required", errObj["code"])
	}
}

func TestDeleteHandler_WrongConfirmHeader(t *testing.T) {
	h := NewDeleteHandler(nil, nil)

	r := newAuthRequest(http.MethodPost, "/v1/tenants/data-delete", "tnt_test", "owner")
	r.Header.Set("X-Confirm-Delete", "DELETE wrong_tenant")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDeleteHandler_Success202(t *testing.T) {
	h := NewDeleteHandler(nil, nil)

	r := newAuthRequest(http.MethodPost, "/v1/tenants/data-delete", "tnt_test", "owner")
	r.Header.Set("X-Confirm-Delete", "DELETE tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "accepted" {
		t.Errorf("status = %q, want accepted", resp["status"])
	}
	if resp["tenant_id"] != "tnt_test" {
		t.Errorf("tenant_id = %q, want tnt_test", resp["tenant_id"])
	}
}

func TestDeleteHandler_ForbiddenForViewer(t *testing.T) {
	h := NewDeleteHandler(nil, nil)

	r := newAuthRequest(http.MethodPost, "/v1/tenants/data-delete", "tnt_test", "viewer")
	r.Header.Set("X-Confirm-Delete", "DELETE tnt_test")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestDeleteHandler_MethodNotAllowed(t *testing.T) {
	h := NewDeleteHandler(nil, nil)

	r := newAuthRequest(http.MethodGet, "/v1/tenants/data-delete", "tnt_test", "owner")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}
