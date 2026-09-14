package domain

// Role is a user's permission tier (PRD F1.2). Roles are ordered from most to
// least privileged; keep the DB enum in sync (migration 000001).
type Role string

const (
	RoleOwner      Role = "OWNER"
	RoleStrategist Role = "STRATEGIST"
	RoleOperator   Role = "OPERATOR"
	RoleAnalyst    Role = "ANALYST"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleStrategist, RoleOperator, RoleAnalyst:
		return true
	}
	return false
}

// Permission is a capability checked by the RBAC middleware (PRD F1.4).
type Permission string

const (
	PermRead   Permission = "read"   // view dashboards, accounts, jobs, reports
	PermAct    Permission = "act"    // queue/approve actions, manage workers
	PermExport Permission = "export" // export data / reports
	PermAdmin  Permission = "admin"  // users, settings, destructive ops
)

// rolePermissions is the single source of truth mapping roles to permissions.
// owner is implicit superset and handled in Can.
var rolePermissions = map[Role]map[Permission]bool{
	RoleStrategist: {PermRead: true},
	RoleOperator:   {PermRead: true, PermAct: true},
	RoleAnalyst:    {PermRead: true, PermExport: true},
}

// Can reports whether the role holds the permission. Owner holds everything.
func (r Role) Can(p Permission) bool {
	if r == RoleOwner {
		return true
	}
	return rolePermissions[r][p]
}
