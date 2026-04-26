package auth

// Role represents a tenant membership role with hierarchical permissions.
type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleViewer    Role = "viewer"
)

// roleLevel maps each role to its numeric privilege level.
// Higher numbers grant more access.
var roleLevel = map[Role]int{
	RoleOwner:     40,
	RoleAdmin:     30,
	RoleDeveloper: 20,
	RoleViewer:    10,
}

// HasPermission returns true when userRole meets or exceeds requiredRole
// in the hierarchy: owner > admin > developer > viewer.
// Unknown roles always return false (fail closed).
func HasPermission(userRole, requiredRole Role) bool {
	uLevel, uOk := roleLevel[userRole]
	rLevel, rOk := roleLevel[requiredRole]
	if !uOk || !rOk {
		return false
	}
	return uLevel >= rLevel
}

// ValidRole returns true if s is a recognised role string.
func ValidRole(s string) bool {
	_, ok := roleLevel[Role(s)]
	return ok
}
