package service

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("hash must not equal plaintext")
	}
	if !verifyPassword("correct horse battery staple", hash) {
		t.Error("verify should accept the correct password")
	}
	if verifyPassword("wrong password", hash) {
		t.Error("verify should reject a wrong password")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	h1, _ := hashPassword("same")
	h2, _ := hashPassword("same")
	if h1 == h2 {
		t.Error("two hashes of the same password must differ (unique salt)")
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", "not-a-hash", "$argon2id$v=19$m=1$c2FsdA$a2V5"} {
		if verifyPassword("x", bad) {
			t.Errorf("malformed hash %q must not verify", bad)
		}
	}
}
