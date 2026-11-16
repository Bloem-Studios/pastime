package main

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/notify"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestRecoveryOpensApplicationWrittenSecret(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, err := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	if err != nil {
		t.Fatal(err)
	}
	s := notify.Store{Pool: f.Pool, Box: box}
	if _, err = s.SaveTarget(ctx, f.Owner, 0, notify.TargetInput{Kind: "webhook", Name: "Recovery", Config: notify.Config{URL: "https://example.invalid/notice", Secret: "synthetic"}}); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	if err = checkSecrets(ctx, f.Pool, box, counts); err != nil {
		t.Fatal(err)
	}
	if counts["secrets_opened"] != 1 {
		t.Fatal(counts)
	}
	wrong, _ := sealed.New("AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	if err = checkSecrets(ctx, f.Pool, wrong, map[string]int{}); err == nil {
		t.Fatal("wrong key accepted")
	}
}
