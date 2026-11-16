package sealed

import (
	"bytes"
	"testing"
)

func TestSealBindsPurposeAndRejectsTampering(t *testing.T) {
	b, err := New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	if err != nil {
		t.Fatal(err)
	}
	c, err := b.Seal([]byte("webhook-secret"), "notify:1")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := b.Open(c, "notify:1"); err != nil || !bytes.Equal(p, []byte("webhook-secret")) {
		t.Fatal("round trip failed", err)
	}
	if _, err = b.Open(c, "notify:2"); err == nil {
		t.Fatal("opened under another purpose")
	}
	c[len(c)-1] ^= 1
	if _, err = b.Open(c, "notify:1"); err == nil {
		t.Fatal("tampered value opened")
	}
	var none *Box
	if _, err = none.Seal([]byte("x"), "p"); err != ErrNoKey || none.Available() {
		t.Fatal("missing key not reported")
	}
	if _, err = New("short"); err == nil {
		t.Fatal("bad key accepted")
	}
}

func TestKeyRotationReadsOldWritesCurrent(t *testing.T) {
	oldKey := "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	newKey := "AQECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	old, _ := New(oldKey)
	data, _ := old.Seal([]byte("synthetic"), "purpose")
	rotated, err := NewKeyring(newKey, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := rotated.Open(data, "purpose"); err != nil || string(plain) != "synthetic" {
		t.Fatal("old key unavailable", err)
	}
	fresh, _ := rotated.Seal([]byte("new"), "purpose")
	if _, err = old.Open(fresh, "purpose"); err == nil {
		t.Fatal("new writes use old key")
	}
	current, _ := New(newKey)
	if _, err = current.Open(fresh, "purpose"); err != nil {
		t.Fatal(err)
	}
	if _, err = rotated.Open(data, "another"); err == nil {
		t.Fatal("purpose binding lost")
	}
}

func TestRotationReadsLegacyV1Ciphertext(t *testing.T) {
	oldKey := "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	old, _ := New(oldKey)
	nonce := bytes.Repeat([]byte{7}, old.aead.NonceSize()) // Synthetic deterministic fixture only.
	legacy := old.aead.Seal(append([]byte{1}, nonce...), nonce, []byte("legacy"), []byte("purpose"))
	ring, err := NewKeyring("AQECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", oldKey)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ring.Open(legacy, "purpose")
	if err != nil || string(plain) != "legacy" {
		t.Fatal("legacy rotation failed", err)
	}
	if _, err = ring.Open(legacy, "wrong"); err == nil {
		t.Fatal("legacy purpose not enforced")
	}
}
