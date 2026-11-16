package imports

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKomgaReadsBooksAndGroupsComicSeries(t *testing.T) {
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "synthetic" || r.Method != "POST" || r.URL.Path != "/api/v1/books/list" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"IN_PROGRESS"`) || !strings.Contains(string(body), `"READ"`) {
			t.Errorf("read status filter missing: %s", body)
		}
		pages++
		if r.URL.Query().Get("page") == "0" {
			w.Write([]byte(`{"last":false,"content":[
				{"id":"b1","seriesId":"s1","seriesTitle":"Synthetic Novel Series","name":"vol1","metadata":{"title":"Synthetic Novel","isbn":"9780000000002","authors":[{"name":"A. Writer","role":"writer"}]},"media":{"pagesCount":200,"mediaProfile":"EPUB"},"readProgress":{"page":50,"completed":false,"readDate":"2030-01-01T00:00:00Z"}},
				{"id":"c1","seriesId":"s2","seriesTitle":"Synthetic Comic","name":"#1","metadata":{"title":"#1"},"media":{"pagesCount":24,"mediaProfile":"DIVINA"},"readProgress":{"page":24,"completed":true,"readDate":"2030-01-02T00:00:00Z"}}]}`))
			return
		}
		w.Write([]byte(`{"last":true,"content":[
			{"id":"c2","seriesId":"s2","seriesTitle":"Synthetic Comic","name":"#2","metadata":{"title":"#2"},"media":{"pagesCount":24,"mediaProfile":"DIVINA"},"readProgress":{"page":3,"completed":false,"readDate":"2030-01-03T00:00:00Z"}},
			{"id":"b2","seriesId":"s3","seriesTitle":"Finished","name":"Finished Book","metadata":{"title":"Finished Book","isbn":"9780000000019"},"media":{"pagesCount":100,"mediaProfile":"EPUB"},"readProgress":{"page":100,"completed":true,"readDate":"2030-01-04T00:00:00Z"}}]}`))
	}))
	defer srv.Close()
	records, err := fetchKomga(context.Background(), srv.Client(), &LinkConfig{URL: srv.URL, Token: "synthetic"})
	if err != nil || pages != 2 || len(records) != 3 {
		t.Fatal(pages, records, err)
	}
	novel, finished, comic := records[0], records[1], records[2]
	if novel.Kind != "book" || novel.Book.ISBN != "9780000000002" || novel.Book.Author != "A. Writer" || novel.Fraction == nil || *novel.Fraction != 0.25 || novel.Status != "in_progress" {
		t.Fatalf("novel %+v", novel)
	}
	if finished.Status != "completed" || len(finished.Plays) != 1 || finished.Plays[0].ExternalID != "komga:b2" {
		t.Fatalf("finished %+v", finished)
	}
	if comic.Kind != "comic" || comic.Title.Title != "Synthetic Comic" || *comic.Progress != 1 {
		t.Fatalf("comic %+v", comic)
	}
}

func TestKavitaSignsInAndMapsSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/Plugin/authenticate":
			if r.URL.Query().Get("apiKey") != "synthetic" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"token":"jwt"}`))
		case "/api/Series/all-v2":
			if r.Header.Get("Authorization") != "Bearer jwt" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`[
				{"id":1,"name":"Synthetic Manga","format":1,"pages":100,"pagesRead":40,"aniListId":30013},
				{"id":2,"name":"Synthetic Epub","format":3,"pages":300,"pagesRead":300,"latestReadDate":"2030-02-01T00:00:00Z"},
				{"id":3,"name":"Synthetic Comic","format":1,"pages":20,"pagesRead":5,"comicVineId":"4050-12345"},
				{"id":4,"name":"Unread","format":1,"pages":20,"pagesRead":0}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	records, err := fetchKavita(context.Background(), srv.Client(), &LinkConfig{URL: srv.URL, Token: "synthetic"})
	if err != nil || len(records) != 3 {
		t.Fatal(records, err)
	}
	if records[0].Kind != "manga" || records[0].Direct.Provider != "anilist" || records[0].Direct.ID != "30013" || *records[0].Fraction != 0.4 {
		t.Fatalf("manga %+v", records[0])
	}
	if records[1].Kind != "book" || records[1].Status != "completed" || len(records[1].Plays) != 1 {
		t.Fatalf("book %+v", records[1])
	}
	if records[2].Kind != "comic" || records[2].Direct.ID != "12345" {
		t.Fatalf("comic %+v", records[2])
	}
	if _, err = fetchKavita(context.Background(), srv.Client(), &LinkConfig{URL: srv.URL, Token: "wrong"}); !errors.Is(err, ErrCredentials) {
		t.Fatal("bad key", err)
	}
}

func TestKapowarrRecordsDownloadedIssuesAsCopies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "synthetic" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"ApiKeyInvalid","result":{}}`))
			return
		}
		switch r.URL.Path {
		case "/api/volumes":
			w.Write([]byte(`{"error":null,"result":[{"id":1,"comicvine_id":777,"title":"Synthetic Volume","issues_downloaded":1},{"id":2,"comicvine_id":778,"title":"Nothing Yet","issues_downloaded":0}]}`))
		case "/api/volumes/1":
			w.Write([]byte(`{"error":null,"result":{"issues":[{"comicvine_id":9001,"issue_number":"1","files":[{"id":5}]},{"comicvine_id":9002,"issue_number":"2","files":[]}]}}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	records, err := fetchKapowarr(context.Background(), srv.Client(), &LinkConfig{URL: srv.URL, Token: "synthetic"})
	if err != nil || len(records) != 1 || records[0].Direct.ID != "777" || len(records[0].Copies) != 1 {
		t.Fatal(records, err)
	}
	c := records[0].Copies[0]
	if c.ExternalID != "777:issue:9001" || c.ChildID != "9001" || c.Store != "Kapowarr" || c.Edition != "#1" {
		t.Fatalf("copy %+v", c)
	}
	if _, err = fetchKapowarr(context.Background(), srv.Client(), &LinkConfig{URL: srv.URL, Token: "wrong"}); !errors.Is(err, ErrCredentials) {
		t.Fatal("bad key", err)
	}
}
