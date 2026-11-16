package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/testdb"
)

type criticTransport func(*http.Request) (*http.Response, error)

func (f criticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCriticCacheQuotaAndAmbiguity(t *testing.T) {
	pool := testdb.Isolated(t)
	ctx := context.Background()
	var id int64
	if err := pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('manual','critic-test','game','Synthetic Game','minutes') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	calls := 0
	mode := "success"
	service := Service{Pool: pool, OpenCriticKey: "synthetic", CriticHTTP: &http.Client{Transport: criticTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"id":77,"topCriticScore":85,"percentRecommended":90,"tier":"Mighty"}`
		status := 200
		if strings.Contains(r.URL.Path, "search") {
			body = `[{"id":77,"name":"Synthetic Game"}]`
			if mode == "ambiguous" {
				body = `[{"id":77,"name":"Synthetic Game"},{"id":78,"name":"Synthetic Game"}]`
			}
		}
		if mode == "quota" {
			status = 429
			body = `{}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"7200"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	result, err := service.Critic(ctx, id)
	if err != nil || result.Status != "pending" || calls != 0 {
		t.Fatal(result, err, calls)
	}
	payload := []byte(fmt.Sprintf(`{"catalog_id":"%d"}`, id))
	if err = service.CriticHandler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	result, err = service.Critic(ctx, id)
	if err != nil || result.Score == nil || *result.Score != 85 || calls != 2 {
		t.Fatal(result, err, calls)
	}
	mode = "quota"
	if err = service.CriticHandler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	before := calls
	if err = service.CriticHandler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if calls != before {
		t.Fatal("ignored shared backoff")
	}
	result, err = service.Critic(ctx, id)
	if err != nil || result.Status != "quota_limited" || result.Score == nil {
		t.Fatal(result, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE critic_quota SET next_allowed=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	mode = "ambiguous"
	before = calls
	if err = service.CriticHandler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	result, err = service.Critic(ctx, id)
	if err != nil || result.Status != "ambiguous" || calls != before+1 {
		t.Fatal(result, err, calls)
	}
}
