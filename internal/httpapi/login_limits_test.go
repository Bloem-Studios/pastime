package httpapi

import (
	"context"
	"testing"
	"time"
)

func TestLoginAccountBudgetSharedAcrossCase(t *testing.T) {
	h := application(t)
	for i := 0; i < 12; i++ {
		if err := h.server.accounts.Limit(context.Background(), accountBucket("login-account", " Someone "), 12, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.server.accounts.Limit(context.Background(), accountBucket("login-account", "someone"), 12, time.Minute); err == nil {
		t.Fatalf("limit=%v", err)
	}
}
