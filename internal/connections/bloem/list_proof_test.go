package bloem

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestListSnapshotCompletionProofHasSourceSignature(t *testing.T) {
	f, s, raw, _, actor, source := calendarFixture(t)
	l, err := (library.Store{Pool: f.Pool}).SaveList(t.Context(), actor, 0, testkit.Key(t, f), library.ListInput{Name: "Signed list", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	in := ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/lists/" + strconv.FormatInt(l.ID, 10)}
	r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", in)
	if err != nil {
		t.Fatal(err)
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(r.SigningKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		t.Fatal("source signing public key missing")
	}
	q := ListSnapshotInput{InstanceID: in.InstanceID, AccountID: in.AccountID, ProfileID: in.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
	p, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := s.AttestListSnapshot(t.Context(), raw, ListAttestationInput{ListSnapshotInput: q, SnapshotID: p.SnapshotID})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{p.SourceAttestation, proof.SourceAttestation} {
		pieces := strings.Split(token, ".")
		if len(pieces) != 3 || pieces[0] != "lp1" {
			t.Fatal("source attestation is not a verifiable signed envelope")
		}
		body, e := base64.RawURLEncoding.Strict().DecodeString(pieces[1])
		if e != nil {
			t.Fatal(e)
		}
		sig, e := base64.RawURLEncoding.Strict().DecodeString(pieces[2])
		if e != nil {
			t.Fatal(e)
		}
		if !ed25519.Verify(key, append([]byte("bloem.list.proof.v1\x00"), body...), sig) {
			t.Fatal("completion signature not valid for preview key")
		}
		var claims struct {
			Version  int       `json:"version"`
			Instance string    `json:"instance_id"`
			Account  string    `json:"account_id"`
			Profile  string    `json:"profile_id"`
			Source   string    `json:"source_id"`
			Snapshot string    `json:"snapshot_id"`
			Digest   string    `json:"digest"`
			Issued   time.Time `json:"issued_at"`
			Expires  time.Time `json:"expires_at"`
		}
		if e = json.Unmarshal(body, &claims); e != nil {
			t.Fatal(e)
		}
		if claims.Version != 1 || claims.Instance != in.InstanceID || claims.Account != in.AccountID || claims.Profile != in.ProfileID || claims.Source != r.SourceID || claims.Snapshot != p.SnapshotID || claims.Digest != p.Digest || claims.Issued.Before(p.GeneratedAt) || claims.Issued.After(time.Now()) || !claims.Expires.Equal(p.ExpiresAt) {
			t.Fatal("completion proof is not bound to the exact source snapshot")
		}
	}
	r2, err := s.ResolveList(t.Context(), raw, "https://pastime.example", in)
	if err != nil || r2.SigningKey != r.SigningKey {
		t.Fatal("source signing key changed on a second resolution", err)
	}
}

func TestListSnapshotConcurrentFirstSignerKeepsOnePublicIdentity(t *testing.T) {
	f, s, raw, _, actor, source := calendarFixture(t)
	list, err := (library.Store{Pool: f.Pool}).SaveList(t.Context(), actor, 0, testkit.Key(t, f), library.ListInput{Name: "Concurrent signer", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	in := ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/lists/" + strconv.FormatInt(list.ID, 10)}
	keys := make([]string, 16)
	errs := make([]error, 16)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Go(func() {
			result, err := s.ResolveList(t.Context(), raw, "https://pastime.example", in)
			keys[i], errs[i] = result.SigningKey, err
		})
	}
	wg.Wait()
	for i, key := range keys {
		if errs[i] != nil || key == "" || key != keys[0] {
			t.Fatalf("initial signer changed or concurrent resolve failed at %d: %v", i, errs[i])
		}
	}
	var count int
	if err = f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_list_signing_keys WHERE instance_id=$1`, source.InstanceID).Scan(&count); err != nil || count != 1 {
		t.Fatal("signer duplicated", count, err)
	}
}
