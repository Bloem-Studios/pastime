package library_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func catalogItem(t *testing.T, f *testkit.Fixture, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := f.Pool.QueryRow(t.Context(), sql, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

type playRow struct {
	catalogID int64
	minutes   *int
	source    string
	playedAt  time.Time
	details   map[string]any
}

func playsFor(t *testing.T, f *testkit.Fixture, entryID int64) []playRow {
	t.Helper()
	rows, err := f.Pool.Query(t.Context(), `SELECT catalog_item_id,minutes,source,played_at,details FROM plays WHERE profile_id=$1 AND entry_id=$2 ORDER BY id`, f.Owner.TrackingID(), entryID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []playRow
	for rows.Next() {
		var p playRow
		var raw []byte
		if err = rows.Scan(&p.catalogID, &p.minutes, &p.source, &p.playedAt, &raw); err != nil {
			t.Fatal(err)
		}
		if raw != nil {
			if err = json.Unmarshal(raw, &p.details); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, p)
	}
	return out
}

func TestCheckInFilmFinishesWithRealTimeAndOneActivePerTitle(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	film := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes) VALUES(0,'movie','tmdb','603','Film','minutes',136,136) RETURNING id`)
	m, err := s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: film})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: film}); fault.Public(err).Code != "already_checked_in" {
		t.Fatal("second active check-in on one title", err)
	}
	active, err := s.ActiveCheckins(ctx, f.Owner)
	if err != nil || len(active) != 1 || active[0].Kind != "play" || active[0].ExpectedEndAt == nil || active[0].Title != "Film" || active[0].MediaKind != "movie" {
		t.Fatalf("active %+v %v", active, err)
	}
	entry, err := s.EntryForCatalog(ctx, f.Owner, film)
	if err != nil || entry == nil || entry.Status != "in_progress" {
		t.Fatalf("entry %+v %v", entry, err)
	}
	minutes := 95
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Minutes: &minutes}); err != nil {
		t.Fatal(err)
	}
	plays := playsFor(t, f, entry.ID)
	if len(plays) != 1 || plays[0].source != library.CheckinSource || plays[0].minutes == nil || *plays[0].minutes != 95 {
		t.Fatalf("plays %+v", plays)
	}
	if entry, _ = s.EntryForCatalog(ctx, f.Owner, film); entry.Status != "completed" {
		t.Fatalf("film not completed: %s", entry.Status)
	}
	if active, _ = s.ActiveCheckins(ctx, f.Owner); len(active) != 0 {
		t.Fatal("finished check-in still active")
	}
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{}); err == nil {
		t.Fatal("finished twice")
	}
	// A new check-in on the same title is allowed once the first is done.
	if _, err = s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: film}); err != nil {
		t.Fatal("rewatch check-in", err)
	}
}

func TestOverdueFilmAndEpisodeCheckinsFinishOnTheirOwn(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	film := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes) VALUES(0,'movie','tmdb','604','Sequel','minutes',90,90) RETURNING id`)
	show := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,runtime_minutes) VALUES(0,'series','tmdb','1399','Show','episodes',50) RETURNING id`)
	season := catalogItem(t, f, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number) VALUES('tmdb','3624','season','Season 1','episodes',$1,1) RETURNING id`, show)
	episode := catalogItem(t, f, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number) VALUES('tmdb','63056','episode','Pilot','episodes',$1,1,1) RETURNING id`, season)
	started := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	for _, id := range []int64{film, episode} {
		if _, err := s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: id, StartedAt: &started}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.FinishDueCheckins(ctx, 10)
	if err != nil || n != 2 {
		t.Fatalf("finished %d %v", n, err)
	}
	filmEntry, _ := s.EntryForCatalog(ctx, f.Owner, film)
	if p := playsFor(t, f, filmEntry.ID); len(p) != 1 || *p[0].minutes != 90 || !p[0].playedAt.Equal(started.Add(90*time.Minute)) {
		t.Fatalf("film play %+v", p)
	}
	showEntry, _ := s.EntryForCatalog(ctx, f.Owner, show)
	if p := playsFor(t, f, showEntry.ID); len(p) != 1 || p[0].catalogID != episode || *p[0].minutes != 50 {
		t.Fatalf("episode play %+v", p)
	}
	if showEntry.Progress != 1 || showEntry.Status != "in_progress" {
		t.Fatalf("show progress %d %s", showEntry.Progress, showEntry.Status)
	}
	if n, _ = s.FinishDueCheckins(ctx, 10); n != 0 {
		t.Fatal("finished again", n)
	}
}

func TestReadingSessionsMoveProgressAndCompleteOnlyWhenDone(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	book := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'book','openlibrary','OL1W','Book','pages',300) RETURNING id`)
	m, err := s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: book})
	if err != nil {
		t.Fatal(err)
	}
	pages, minutes := 40, 30
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Amount: &pages, Minutes: &minutes}); err != nil {
		t.Fatal(err)
	}
	entry, _ := s.EntryForCatalog(ctx, f.Owner, book)
	if entry.Progress != 40 || entry.Status != "in_progress" || len(playsFor(t, f, entry.ID)) != 0 {
		t.Fatalf("after one session: progress %d status %s plays %d", entry.Progress, entry.Status, len(playsFor(t, f, entry.ID)))
	}
	got, err := s.GetCheckin(ctx, f.Owner, m.ID)
	if err != nil || got.Status != "finished" || got.Amount == nil || *got.Amount != 40 || *got.Minutes != 30 || got.Kind != "session" {
		t.Fatalf("session %+v %v", got, err)
	}
	m, _ = s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: book})
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Completed: true, Minutes: &minutes}); err != nil {
		t.Fatal(err)
	}
	entry, _ = s.EntryForCatalog(ctx, f.Owner, book)
	plays := playsFor(t, f, entry.ID)
	if entry.Status != "completed" || entry.Progress != 300 || len(plays) != 1 || plays[0].source != library.CheckinSource {
		t.Fatalf("finished book: status %s progress %d plays %+v", entry.Status, entry.Progress, plays)
	}
	film := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','tmdb','605','Third','minutes',100) RETURNING id`)
	m, _ = s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: film})
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Amount: &pages}); err == nil {
		t.Fatal("pages accepted on a film")
	}
}

func TestBoardGameAndGameDetails(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	board := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'boardgame','bgg','13','Catan','items') RETURNING id`)
	game := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'game','igdb','1942','Witcher','items') RETURNING id`)
	m, err := s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: board})
	if err != nil {
		t.Fatal(err)
	}
	ten, seven := 10.0, 7.0
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{
		Players:  []library.BoardGamePlayer{{Name: " Ann ", Score: &ten, Winner: true}, {Name: "Ben", Score: &seven}},
		Location: "Home",
	}); err != nil {
		t.Fatal(err)
	}
	entry, _ := s.EntryForCatalog(ctx, f.Owner, board)
	plays := playsFor(t, f, entry.ID)
	if len(plays) != 1 || plays[0].details["location"] != "Home" {
		t.Fatalf("board game play %+v", plays)
	}
	players, _ := plays[0].details["players"].([]any)
	first, _ := players[0].(map[string]any)
	if len(players) != 2 || first["name"] != "Ann" || first["winner"] != true || first["score"] != 10.0 {
		t.Fatalf("players %+v", players)
	}
	m, _ = s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: game})
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Players: []library.BoardGamePlayer{{Name: "Ann"}}}); err == nil {
		t.Fatal("players accepted on a video game")
	}
	two := 120
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Platform: "Switch", Minutes: &two}); err != nil {
		t.Fatal(err)
	}
	gameEntry, _ := s.EntryForCatalog(ctx, f.Owner, game)
	got, _ := s.GetCheckin(ctx, f.Owner, m.ID)
	if gameEntry.Status != "in_progress" || len(playsFor(t, f, gameEntry.ID)) != 0 || got.Details["platform"] != "Switch" || *got.Minutes != 120 {
		t.Fatalf("game session: status %s details %+v", gameEntry.Status, got.Details)
	}
	m, _ = s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: game})
	if _, err = s.CancelCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f)); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetCheckin(ctx, f.Owner, m.ID); got.Status != "cancelled" || got.FinishedAt == nil {
		t.Fatalf("cancelled %+v", got)
	}
	if _, err = s.CancelCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f)); err == nil {
		t.Fatal("cancelled twice")
	}
}

func TestGameSessionTimeAddsToPlaytime(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	game := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'game','igdb','1943','Hades','minutes') RETURNING id`)
	for _, spent := range []int{90, 45} {
		m, err := s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: game})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Minutes: &spent, Platform: "PC"}); err != nil {
			t.Fatal(err)
		}
	}
	entry, _ := s.EntryForCatalog(ctx, f.Owner, game)
	if entry.Progress != 135 || entry.Status != "in_progress" || len(playsFor(t, f, entry.ID)) != 0 {
		t.Fatalf("playtime %d status %s", entry.Progress, entry.Status)
	}
}

func TestImportedPlaysKeepMinutesAndDetails(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	board := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'boardgame','bgg','9209','Ticket to Ride','items') RETURNING id`)
	minutes := 75
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	play := library.ImportPlay{TargetID: board, At: at, ExternalID: "bgg-play:501", Minutes: &minutes,
		Details: map[string]any{"location": "Home", "players": []map[string]any{{"name": "Ann", "winner": true, "score": 10.0}}}}
	for range 2 { // a second sync of the same play changes nothing
		if _, err := s.ApplyImport(ctx, f.Owner.User.ID, "bgg", library.ImportItem{TitleID: board, Status: "completed", Plays: []library.ImportPlay{play}}); err != nil {
			t.Fatal(err)
		}
	}
	entry, _ := s.EntryForCatalog(ctx, f.Owner, board)
	plays := playsFor(t, f, entry.ID)
	if len(plays) != 1 || plays[0].source != "bgg" || plays[0].minutes == nil || *plays[0].minutes != 75 || plays[0].details["location"] != "Home" || !plays[0].playedAt.Equal(at) {
		t.Fatalf("imported plays %+v", plays)
	}
}

func TestPlayHistoryListsPlaysSessionsAndWinners(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := library.Store{Pool: f.Pool}
	board := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'boardgame','bgg','14','Carcassonne','items') RETURNING id`)
	ten, seven, eight := 10.0, 7.0, 8.0
	for _, game := range [][]library.BoardGamePlayer{
		{{Name: "Ann", Score: &ten, Winner: true}, {Name: "Ben", Score: &seven}},
		{{Name: "ann", Score: &seven}, {Name: "Ben", Score: &eight, Winner: true}, {Name: "Cy"}},
		{{Name: "Ann", Winner: true}},
	} {
		m, err := s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: board})
		if err != nil {
			t.Fatal(err)
		}
		thirty := 30
		if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Players: game, Minutes: &thirty}); err != nil {
			t.Fatal(err)
		}
	}
	entry, _ := s.EntryForCatalog(ctx, f.Owner, board)
	h, err := s.PlayHistory(ctx, f.Owner, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h.Plays != 3 || h.Sessions != 0 || h.Minutes != 90 || len(h.Items) != 3 || h.Items[0].Kind != "play" || h.Items[0].Source != library.CheckinSource {
		t.Fatalf("history %+v", h)
	}
	if len(h.Players) != 3 || h.Players[0].Name != "Ann" || h.Players[0].Plays != 3 || h.Players[0].Wins != 2 || h.Players[1].Name != "Ben" || h.Players[1].Wins != 1 || h.Players[2].Name != "Cy" {
		t.Fatalf("players %+v", h.Players)
	}
	game := catalogItem(t, f, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'game','igdb','1944','Celeste','minutes') RETURNING id`)
	m, _ := s.CheckIn(ctx, f.Owner, testkit.Key(t, f), library.CheckinInput{CatalogID: game})
	hour := 60
	if _, err = s.FinishCheckin(ctx, f.Owner, m.ID, testkit.Key(t, f), library.FinishInput{Minutes: &hour, Platform: "Switch"}); err != nil {
		t.Fatal(err)
	}
	gameEntry, _ := s.EntryForCatalog(ctx, f.Owner, game)
	if h, err = s.PlayHistory(ctx, f.Owner, gameEntry.ID); err != nil || h.Sessions != 1 || h.Plays != 0 || h.Minutes != 60 || h.Items[0].Details["platform"] != "Switch" {
		t.Fatalf("game history %+v %v", h, err)
	}
	if _, err = s.PlayHistory(ctx, f.Owner, 999999); err == nil {
		t.Fatal("unknown entry")
	}
}
