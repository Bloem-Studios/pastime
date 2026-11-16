package identity

import (
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1: at T=59s the eight-digit code is 94287082.
func TestTOTPMatchesRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")
	if got := totpCode(secret, 59/30); got != "287082" {
		t.Fatalf("code %s", got)
	}
	step, ok := verifyTOTP(secret, "287082", time.Unix(59, 0), 0)
	if !ok || step != 1 {
		t.Fatal("valid code rejected")
	}
	if _, ok = verifyTOTP(secret, "287082", time.Unix(59, 0), step); ok {
		t.Fatal("code replayed")
	}
	if _, ok = verifyTOTP(secret, "287082", time.Unix(59+300, 0), 0); ok {
		t.Fatal("stale code accepted")
	}
}
