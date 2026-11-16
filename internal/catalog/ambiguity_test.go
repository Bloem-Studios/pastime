package catalog_test

import (
	"context"
	"errors"
	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"testing"
)

type ambiguousTV struct{ fakeTV }

func (*ambiguousTV) Search(context.Context, string, string) ([]provider.Record, error) {
	return []provider.Record{{Provider: "faketv", Kind: "movie", ProviderID: "1", Title: "Same title"}, {Provider: "faketv", Kind: "movie", ProviderID: "2", Title: "Same title"}}, nil
}
func TestAmbiguousTitleRequiresManualChoice(t *testing.T) {
	_, s, _ := setup(t)
	s.Providers["faketv"] = &ambiguousTV{}
	_, err := s.MatchTitle(context.Background(), catalog.TitleReference{Kind: "movie", Title: "Same title"})
	if !errors.Is(err, catalog.ErrNoMatch) {
		t.Fatal("ambiguous title auto-matched", err)
	}
}

type orderedTV struct {
	fakeTV
	reverse bool
}

func (*orderedTV) EpisodeOrders(context.Context, string) ([]provider.EpisodeOrder, error) {
	return []provider.EpisodeOrder{{ID: "owned", Name: "Owned order"}}, nil
}
func (o *orderedTV) EpisodeOrder(context.Context, string) (provider.EpisodeOrder, error) {
	if o.reverse {
		return provider.EpisodeOrder{ID: "owned", Name: "Owned order", Groups: []provider.EpisodeOrderGroup{{Name: "First", Episodes: []string{"100-1-1", "100-1-2"}}}}, nil
	}
	return provider.EpisodeOrder{ID: "owned", Name: "Owned order", Groups: []provider.EpisodeOrderGroup{{Name: "First", Episodes: []string{"100-1-2", "100-1-1"}}}}, nil
}
func TestEpisodeOrderMustBelongToShow(t *testing.T) {
	_, s, _ := setup(t)
	s.Providers["faketv"] = &orderedTV{}
	ctx := context.Background()
	show, err := s.Resolve(ctx, "faketv", "series", "100")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EpisodeOrder(ctx, show.ID, "faketv:someone-elses"); err == nil {
		t.Fatal("accepted unrelated order")
	}
	if _, err = s.EpisodeOrder(ctx, show.ID, "faketv:owned"); err != nil {
		t.Fatal(err)
	}
}

type conflictingTV struct{ fakeTV }

func (*conflictingTV) Find(context.Context, string, string) (provider.Found, error) {
	return provider.Found{Movies: []string{"2"}}, nil
}
func TestConflictingStableIdentifiersRequireRepair(t *testing.T) {
	_, s, _ := setup(t)
	s.Providers["tmdb"] = &conflictingTV{}
	_, err := s.MatchVideo(context.Background(), catalog.Reference{Kind: "movie", TMDB: "1", IMDB: "tt2"})
	if !errors.Is(err, catalog.ErrNoMatch) {
		t.Fatal("conflicting identifiers silently preferred", err)
	}
}

func TestOrderRefreshUpdatesExistingFollowers(t *testing.T) {
	f, s, _ := setup(t)
	o := &orderedTV{}
	s.Providers["faketv"] = o
	ctx := context.Background()
	show, err := s.Resolve(ctx, "faketv", "series", "100")
	if err != nil {
		t.Fatal(err)
	}
	episodes(t, s, show)
	order, err := s.EpisodeOrder(ctx, show.ID, "faketv:owned")
	if err != nil {
		t.Fatal(err)
	}
	first := order.Groups[0].Episodes[0].ID
	var entry int64
	if err = f.Pool.QueryRow(ctx, `INSERT INTO library_entries(user_id,catalog_item_id,title,media_type,unit,status,episode_order,next_item_id) VALUES($1,$2,'Order follower','series','episodes','planned','faketv:owned',$3) RETURNING id`, f.Owner.User.ID, show.ID, first).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	o.reverse = true
	if _, err = f.Pool.Exec(ctx, "UPDATE episode_orders SET refreshed_at=clock_timestamp()-interval '8 days'"); err != nil {
		t.Fatal(err)
	}
	updated, err := s.EpisodeOrder(ctx, show.ID, "faketv:owned")
	if err != nil {
		t.Fatal(err)
	}
	var next int64
	if err = f.Pool.QueryRow(ctx, "SELECT next_item_id FROM library_entries WHERE id=$1", entry).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next == first || next != updated.Groups[0].Episodes[0].ID {
		t.Fatal("projection did not follow refreshed order", next, first)
	}
}
