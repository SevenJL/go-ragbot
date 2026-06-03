package auth

import (
	"testing"
	"time"
)

func TestIssueAndVerify(t *testing.T) {
	iss := NewIssuer("test-secret-key", 1*time.Hour)
	tok, err := iss.Issue("user-1", RoleUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Raw == "" {
		t.Fatal("empty token")
	}
	if tok.Claims.Sub != "user-1" || tok.Claims.Role != RoleUser {
		t.Fatalf("claims: sub=%s role=%s", tok.Claims.Sub, tok.Claims.Role)
	}

	claims, err := iss.Verify(tok.Raw)
	if err != nil {
		t.Fatal("verify:", err)
	}
	if claims.Sub != "user-1" {
		t.Fatalf("sub = %s", claims.Sub)
	}
}

func TestVerifyBadSignature(t *testing.T) {
	iss := NewIssuer("secret-a", 1*time.Hour)
	tok, _ := iss.Issue("u", RoleUser, "")

	iss2 := NewIssuer("secret-b", 1*time.Hour)
	if _, err := iss2.Verify(tok.Raw); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestVerifyExpired(t *testing.T) {
	iss := NewIssuer("secret", 1*time.Millisecond)
	tok, _ := iss.Issue("u", RoleUser, "")
	time.Sleep(5 * time.Millisecond)
	if _, err := iss.Verify(tok.Raw); err == nil {
		t.Fatal("expected expired error")
	}
}

func TestRevoke(t *testing.T) {
	iss := NewIssuer("secret", 1*time.Hour)
	tok, _ := iss.Issue("u", RoleUser, "")
	iss.Revoke("u", tok.Claims.Iat)
	if _, err := iss.Verify(tok.Raw); err == nil {
		t.Fatal("expected revoked error")
	}
}

func TestHasRole(t *testing.T) {
	admin := &Claims{Role: RoleAdmin}
	user := &Claims{Role: RoleUser}

	if !HasRole(admin, RoleAdmin) {
		t.Fatal("admin should have admin role")
	}
	if !HasRole(admin, RoleUser) {
		t.Fatal("admin should have user role (inheritance)")
	}
	if !HasRole(user, RoleUser) {
		t.Fatal("user should have user role")
	}
	if HasRole(user, RoleAdmin) {
		t.Fatal("user should NOT have admin role")
	}
	if HasRole(nil, RoleUser) {
		t.Fatal("nil claims should not have any role")
	}
}

func TestTemplatedUserTokens(t *testing.T) {
	iss := NewIssuer("secret", 1*time.Hour)

	// Issue tokens for different users — each gets a different subject.
	t1, _ := iss.Issue("alice", RoleAdmin, "")
	t2, _ := iss.Issue("bob", RoleUser, "")

	c1, _ := iss.Verify(t1.Raw)
	c2, _ := iss.Verify(t2.Raw)

	if c1.Sub != "alice" || c1.Role != RoleAdmin {
		t.Fatal("alice claims wrong")
	}
	if c2.Sub != "bob" || c2.Role != RoleUser {
		t.Fatal("bob claims wrong")
	}
}
