package auth

import "testing"

func TestHasPermission_Hierarchy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		user     Role
		required Role
		want     bool
	}{
		{RoleOwner, RoleOwner, true},
		{RoleOwner, RoleAdmin, true},
		{RoleOwner, RoleDeveloper, true},
		{RoleOwner, RoleViewer, true},
		{RoleAdmin, RoleOwner, false},
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleDeveloper, true},
		{RoleAdmin, RoleViewer, true},
		{RoleDeveloper, RoleOwner, false},
		{RoleDeveloper, RoleAdmin, false},
		{RoleDeveloper, RoleDeveloper, true},
		{RoleDeveloper, RoleViewer, true},
		{RoleViewer, RoleOwner, false},
		{RoleViewer, RoleAdmin, false},
		{RoleViewer, RoleDeveloper, false},
		{RoleViewer, RoleViewer, true},
	}
	for _, tt := range tests {
		got := HasPermission(tt.user, tt.required)
		if got != tt.want {
			t.Errorf("HasPermission(%q, %q) = %v, want %v", tt.user, tt.required, got, tt.want)
		}
	}
}

func TestHasPermission_InvalidRoles(t *testing.T) {
	t.Parallel()
	// Unknown user role -> deny
	if HasPermission(Role("superadmin"), RoleViewer) {
		t.Error("expected false for unknown user role")
	}
	// Unknown required role -> deny
	if HasPermission(RoleOwner, Role("superviewer")) {
		t.Error("expected false for unknown required role")
	}
	// Both unknown -> deny
	if HasPermission(Role("x"), Role("y")) {
		t.Error("expected false for both unknown roles")
	}
	// Empty strings -> deny
	if HasPermission(Role(""), Role("")) {
		t.Error("expected false for empty roles")
	}
}

func TestValidRole(t *testing.T) {
	t.Parallel()
	valid := []string{"owner", "admin", "developer", "viewer"}
	for _, v := range valid {
		if !ValidRole(v) {
			t.Errorf("ValidRole(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "superadmin", "OWNER", "Owner", "root", "user"}
	for _, v := range invalid {
		if ValidRole(v) {
			t.Errorf("ValidRole(%q) = true, want false", v)
		}
	}
}
