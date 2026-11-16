package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// The statistics, year and review responses match the contract with a play and a
// reading session recorded; every response is checked against it by the harness.
func TestStatisticsAndYearInReviewContracts(t *testing.T) {
	h := application(t)
	origin := "http://127.0.0.1:8080"
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		return h.request(method, path, body, h.OwnerRaw, h.Owner.CSRF, origin, testkit.Key(t, h.Fixture))
	}
	catalog := func(sql string) string {
		var id int64
		if err := h.Pool.QueryRow(t.Context(), sql).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return strconv.FormatInt(id, 10)
	}
	book := catalog(`INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,credits) VALUES(0,'book','openlibrary','OL9W','Contract Book','pages',200,'{"Ann Author"}') RETURNING id`)
	film := catalog(`INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes) VALUES(0,'movie','tmdb','9001','Contract Film','minutes',100,100) RETURNING id`)
	for _, step := range []struct {
		catalogID string
		finish    map[string]any
	}{{book, map[string]any{"minutes": 25, "amount": 30}}, {film, map[string]any{"minutes": 95}}} {
		started := call("POST", "/api/v1/checkins", map[string]any{"catalog_id": step.catalogID})
		requireCode(t, started, 201)
		var ack struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(started.Body.Bytes(), &ack)
		requireCode(t, call("POST", "/api/v1/checkins/"+ack.ID+"/finish", step.finish), 200)
	}
	stats := call("GET", "/api/v1/stats", nil)
	requireCode(t, stats, 200)
	var insights struct {
		Plays, Sessions, Minutes int
		Streaks                  struct{ Current struct{ Days int } }
	}
	_ = json.Unmarshal(stats.Body.Bytes(), &insights)
	if insights.Plays != 1 || insights.Sessions != 1 || insights.Minutes != 120 || insights.Streaks.Current.Days != 1 {
		t.Fatalf("stats %s", stats.Body.String())
	}
	requireCode(t, call("GET", "/api/v1/stats/years", nil), 200)
	year := time.Now().UTC().Year()
	for _, y := range []int{year, year - 1} {
		requireCode(t, call("GET", "/api/v1/review/"+strconv.Itoa(y), nil), 200)
	}
	for _, bad := range []string{"abcd", "123", strconv.Itoa(year + 1)} {
		requireCode(t, call("GET", "/api/v1/review/"+bad, nil), 422)
	}
}
