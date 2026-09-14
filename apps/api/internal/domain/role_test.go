package domain

import "testing"

func TestRoleCan(t *testing.T) {
	tests := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleOwner, PermRead, true},
		{RoleOwner, PermAct, true},
		{RoleOwner, PermExport, true},
		{RoleOwner, PermAdmin, true},

		{RoleStrategist, PermRead, true},
		{RoleStrategist, PermAct, false},
		{RoleStrategist, PermExport, false},
		{RoleStrategist, PermAdmin, false},

		{RoleOperator, PermRead, true},
		{RoleOperator, PermAct, true},
		{RoleOperator, PermExport, false},
		{RoleOperator, PermAdmin, false},

		{RoleAnalyst, PermRead, true},
		{RoleAnalyst, PermAct, false},
		{RoleAnalyst, PermExport, true},
		{RoleAnalyst, PermAdmin, false},

		{"UNKNOWN", PermRead, false},
	}
	for _, tt := range tests {
		if got := tt.role.Can(tt.perm); got != tt.want {
			t.Errorf("%s.Can(%s) = %v, want %v", tt.role, tt.perm, got, tt.want)
		}
	}
}

func TestRoleValid(t *testing.T) {
	for _, r := range []Role{RoleOwner, RoleStrategist, RoleOperator, RoleAnalyst} {
		if !r.Valid() {
			t.Errorf("%s should be valid", r)
		}
	}
	if Role("HACKER").Valid() {
		t.Error("unknown role must be invalid")
	}
}
