package imports

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWeTrakrPartialFilesAndDates(t *testing.T) {
	records, err := ParseWeTrakr(zipOf(t, map[string]string{
		"lists/Extra.csv": "type,title,tmdb_id\nmovie,Synthetic A,42\n",
		"history.csv":     "type,title,tmdb_id,watched_at,history_id\nmovie,Synthetic A,42,2020-02-03T04:05:06Z,1\nmovie,Synthetic A,42,2020-02-03T04:05:06Z,1\n",
		"ratings.csv":     "type,title,tmdb_id,rating,rated_at,notes,list_name\nmovie,Synthetic A,42,8,2020-02-04T04:05:06Z,A note,Weekend\n",
	}))
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	r := records[0]
	if len(r.Plays) != 1 || r.Plays[0].At.Year() != 2020 || r.ScoredAt == nil || *r.Rating != 8 || len(r.Lists) != 2 || r.Notes != "A note" {
		t.Fatalf("%+v", r)
	}
	partial, err := ParseWeTrakr([]byte("type,title,tmdb_id,rating\nmovie,Synthetic A,42,8\n"))
	if err != nil || len(partial) != 1 || len(partial[0].Plays) != 0 {
		t.Fatal(partial, err)
	}
	if _, err = ParseWeTrakr([]byte("type,title,watched_at\nmovie,Bad,not-a-date\n")); err == nil {
		t.Fatal("invalid date accepted")
	}
}
func TestNavidromeTrackNumberVariants(t *testing.T) {
	for _, metadata := range []string{`"tracknumber":"2"`, `"tracknumber":2`, `"track_number":"2"`, `"track_number":2`} {
		listens, err := ParseListens([]byte(`{"listen_type":"single","payload":[{"listened_at":1600000000,"track_metadata":{"artist_name":"Synthetic Artist","track_name":"Song","release_name":"Album","additional_info":{`+metadata+`}}}]}`), time.Now())
		if err != nil || len(listens) != 1 || listens[0].TrackNumber == nil || *listens[0].TrackNumber != 2 {
			t.Fatal(listens, err)
		}
	}
	if _, err := listenTrackNumber([]byte(`"2"`), []byte(`3`)); err == nil {
		t.Fatal("conflicting positions accepted")
	}
}
func TestMylarFullScanAndFailures(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "synthetic" {
			t.Error("missing credential")
		}
		if r.URL.Query().Get("cmd") == "getIndex" {
			w.Write([]byte(`{"success":true,"data":[{"id":"7","name":"Synthetic Comic"}]}`))
			return
		}
		if fail {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"success":true,"data":{"issues":[{"id":"11","number":"1","status":"Downloaded"},{"id":"12","number":"2","status":"Wanted"}],"annuals":[{"id":"13","number":"1","status":"Archived"}]}}`))
	}))
	defer srv.Close()
	cfg := LinkConfig{URL: srv.URL, Token: "synthetic"}
	records, err := fetchMylar(context.Background(), srv.Client(), &cfg)
	if err != nil || len(records) != 1 || len(records[0].Copies) != 2 {
		t.Fatal(records, err)
	}
	if records[0].Copies[0].ExternalID == records[0].Copies[1].ExternalID {
		t.Fatal("annual collides with issue")
	}
	fail = true
	if records, err = fetchMylar(context.Background(), srv.Client(), &cfg); err == nil || records != nil {
		t.Fatal("partial scan returned as complete")
	}
}
func TestRemoteHealthClassifiesRejections(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		_, err := getJSON(context.Background(), srv.Client(), "GET", srv.URL, nil, nil, nil)
		srv.Close()
		if (status == 401 || status == 403) != errors.Is(err, ErrCredentials) {
			t.Fatal(status, err)
		}
		if (status == 429) != errors.Is(err, ErrRateLimited) {
			t.Fatal(status, err)
		}
	}
}

func TestMylarReconciliationRequiresSuccess(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	entry, err := lib.Create(ctx, f.Owner, testkit.Key(t, f), library.EntryInput{Title: "Synthetic Comics", MediaType: "comic", Unit: "items", Status: "planned"})
	if err != nil {
		t.Fatal(err)
	}
	var link int64
	if err = f.Pool.QueryRow(ctx, "INSERT INTO service_links(user_id,kind,name,sealed_config,schedule) VALUES($1,'mylar3','Synthetic',decode('00','hex'),'two_hourly') RETURNING id", f.Owner.User.ID).Scan(&link); err != nil {
		t.Fatal(err)
	}
	for _, copy := range [][2]string{{"mylar3", "kept"}, {"mylar3", "removed"}, {"manual", "untouched"}} {
		if _, err = f.Pool.Exec(ctx, "INSERT INTO owned_copies(user_id,entry_id,format,source,external_id) VALUES($1,$2,'digital',$3,$4)", f.Owner.User.ID, entry.ID, copy[0], copy[1]); err != nil {
			t.Fatal(err)
		}
	}
	var run string
	if err = f.Pool.QueryRow(ctx, "INSERT INTO import_runs(user_id,source) VALUES($1,'mylar3') RETURNING id::text", f.Owner.User.ID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]Record{{Copies: []library.ImportCopy{{ExternalID: "kept"}}}})
	if _, err = f.Pool.Exec(ctx, "INSERT INTO import_batches(run_id,user_id,link_id,generation,records) VALUES($1,$2,$3,1,$4)", run, f.Owner.User.ID, link, raw); err != nil {
		t.Fatal(err)
	}
	store := Store{Pool: f.Pool}
	count := func() int {
		var n int
		if err := f.Pool.QueryRow(ctx, "SELECT count(*) FROM owned_copies WHERE user_id=$1", f.Owner.User.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err = store.finish(ctx, run, "partial_success", "", nil); err != nil || count() != 3 {
		t.Fatal("partial scan reconciled", err)
	}
	if err = store.finish(ctx, run, "succeeded", "", nil); err != nil || count() != 2 {
		t.Fatal("complete scan did not reconcile", err)
	}
	if err = store.DeleteLink(ctx, f.Owner, link); err != nil || count() != 1 {
		t.Fatal("disconnect did not isolate source copies", err)
	}
}
