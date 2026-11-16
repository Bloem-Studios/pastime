package imports

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
)

const bggCollectionXML = `<?xml version="1.0" encoding="utf-8"?>
<items totalitems="2">
 <item objecttype="thing" objectid="13" subtype="boardgame"><name sortindex="1">Catan</name>
  <status own="1" prevowned="0" want="0" wanttoplay="0" wanttobuy="0" wishlist="0" preordered="0"/>
  <numplays>1</numplays><stats><rating value="7.6"/></stats></item>
 <item objecttype="thing" objectid="230802" subtype="boardgame"><name sortindex="1">Azul</name>
  <status own="0" prevowned="0" want="0" wanttoplay="1" wanttobuy="0" wishlist="1" preordered="0"/>
  <numplays>0</numplays><stats><rating value="N/A"/></stats></item>
</items>`

const bggPlaysXML = `<?xml version="1.0" encoding="utf-8"?>
<plays username="ann" total="2" page="1">
 <play id="501" date="2026-10-31" quantity="1" length="75" incomplete="0" location="Home">
  <item name="Catan" objecttype="thing" objectid="13"><subtypes><subtype value="boardgame"/></subtypes></item>
  <players><player username="ann" name="Ann" score="10" win="1"/><player username="" name="Ben" score="7" win="0"/></players>
 </play>
 <play id="502" date="2026-11-01" quantity="2" length="0" location="">
  <item name="Ticket to Ride" objecttype="thing" objectid="9209"><subtypes><subtype value="boardgame"/></subtypes></item>
 </play>
</plays>`

func TestFetchBGGReadsCollectionAndPlays(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/collection":
			if r.URL.Query().Get("username") != "ann" || r.URL.Query().Get("excludesubtype") != "boardgameexpansion" {
				t.Errorf("collection query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(bggCollectionXML))
		case "/plays":
			_, _ = w.Write([]byte(bggPlaysXML))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	bggAPI, bggPause = srv.URL+"/", 0
	s := Store{Creds: Credentials{BGGToken: "secret-token"}}
	recs, err := s.fetchBGG(context.Background(), srv.Client(), &LinkConfig{Username: " ann "})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("records %+v", recs)
	}
	catan, azul, ticket := recs[0], recs[1], recs[2]
	if catan.Direct == nil || catan.Direct.Provider != "bgg" || catan.Direct.ID != "13" || catan.Status != "completed" || catan.Rating == nil || *catan.Rating != 8 || len(catan.Copies) != 1 {
		t.Fatalf("catan %+v", catan)
	}
	if len(catan.Plays) != 1 || *catan.Plays[0].Minutes != 75 || catan.Plays[0].ExternalID != "bgg-play:501" || catan.Plays[0].Details["location"] != "Home" {
		t.Fatalf("catan play %+v", catan.Plays)
	}
	players, _ := catan.Plays[0].Details["players"].([]map[string]any)
	if len(players) != 2 || players[0]["name"] != "Ann" || players[0]["winner"] != true || players[0]["score"] != 10.0 || players[1]["winner"] != false {
		t.Fatalf("players %+v", players)
	}
	if azul.Status != "planned" || azul.Rating != nil || len(azul.Copies) != 0 || len(azul.Plays) != 0 {
		t.Fatalf("azul %+v", azul)
	}
	if ticket.Label != "Ticket to Ride" || ticket.Status != "completed" || len(ticket.Plays) != 2 || ticket.Plays[0].Minutes != nil ||
		ticket.Plays[0].ExternalID != "bgg-play:502#1" || ticket.Plays[1].ExternalID != "bgg-play:502#2" {
		t.Fatalf("ticket %+v", ticket)
	}
	for _, a := range auth {
		if a != "Bearer secret-token" {
			t.Fatalf("authorization %q", a)
		}
	}
}

func TestFetchBGGErrors(t *testing.T) {
	status := http.StatusAccepted
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`<errors><error><message>Invalid username specified</message></error></errors>`))
		}
	}))
	defer srv.Close()
	bggAPI, bggPause = srv.URL+"/", 0
	s := Store{Creds: Credentials{BGGToken: "t"}}
	if _, err := s.fetchBGG(context.Background(), srv.Client(), &LinkConfig{Username: "ann"}); !errors.Is(err, provider.ErrUnavailable) {
		t.Fatal("202 should be temporary", err)
	}
	status = http.StatusOK
	if _, err := s.fetchBGG(context.Background(), srv.Client(), &LinkConfig{Username: "nobody"}); !errors.Is(err, ErrFormat) {
		t.Fatal("unknown user", err)
	}
	if _, err := (Store{}).fetchBGG(context.Background(), srv.Client(), &LinkConfig{Username: "ann"}); !errors.Is(err, provider.ErrNotConfigured) {
		t.Fatal("no token", err)
	}
}
