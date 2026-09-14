package adapter

import (
	"testing"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
)

func TestJWTIssueAndVerify(t *testing.T) {
	iss, err := NewJWTIssuer("0123456789abcdef0123456789abcdef", "smm-test")
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	token, err := iss.Issue("user-1", domain.RoleOperator, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := iss.Verify(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.UserID != "user-1" || claims.Role != domain.RoleOperator {
		t.Errorf("claims = %+v, want user-1/OPERATOR", claims)
	}
}

func TestJWTVerifyRejectsWrongSecret(t *testing.T) {
	a, _ := NewJWTIssuer("0123456789abcdef0123456789abcdef", "smm-test")
	b, _ := NewJWTIssuer("ffffffffffffffffffffffffffffffff", "smm-test")
	token, _ := a.Issue("user-1", domain.RoleOwner, time.Minute)
	if _, err := b.Verify(token); err != domain.ErrUnauthorized {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestJWTVerifyRejectsExpired(t *testing.T) {
	iss, _ := NewJWTIssuer("0123456789abcdef0123456789abcdef", "smm-test")
	token, _ := iss.Issue("user-1", domain.RoleAnalyst, -time.Minute)
	if _, err := iss.Verify(token); err != domain.ErrUnauthorized {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestJWTIssuerRejectsShortSecret(t *testing.T) {
	if _, err := NewJWTIssuer("short", "smm-test"); err == nil {
		t.Error("expected error for short secret")
	}
}
