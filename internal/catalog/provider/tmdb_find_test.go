package provider

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TMDB's /find answers every result type at once. TVDB numbers series and episodes
// independently, so a series id can also be another show's episode id; Find keeps the
// types apart so a series lookup never takes the episode's show.
func TestTMDBFindKeepsResultTypesApart(t *testing.T) {
	c := fixture(routes{
		"api.themoviedb.org/3/find/255192": `{"movie_results":[],"tv_results":[{"id":39793,"name":"Call the Midwife"}],
			"tv_episode_results":[{"id":1000133,"show_id":133,"season_number":4,"episode_number":13,"name":"Juliet Bravo episode"}]}`,
		"api.themoviedb.org/3/find/9525822":   `{"movie_results":[],"tv_results":[],"tv_episode_results":[{"id":4173675,"show_id":39793,"season_number":12,"episode_number":5},{"show_id":0,"season_number":1,"episode_number":1}]}`,
		"api.themoviedb.org/3/find/tt0000042": `{"movie_results":[{"id":42}],"tv_results":[],"tv_episode_results":[]}`,
		"api.themoviedb.org/3/find/1":         `{"movie_results":[],"tv_results":[],"tv_episode_results":[]}`,
	})
	p := TMDB{Client: c, Key: "synthetic"}
	ctx := context.Background()
	found, err := p.Find(ctx, "tvdb", "255192")
	if err != nil {
		t.Fatal(err)
	}
	want := Found{Shows: []string{"39793"}, Episodes: []FoundEpisode{{ID: "1000133", ShowID: "133", Season: 4, Episode: 13}}}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("series id colliding with an episode id: %+v", found)
	}
	found, err = p.Find(ctx, "tvdb", "9525822")
	if err != nil || !reflect.DeepEqual(found, Found{Episodes: []FoundEpisode{{ID: "4173675", ShowID: "39793", Season: 12, Episode: 5}}}) {
		t.Fatalf("episode lookup keeps the episode id and drops unusable rows: %+v %v", found, err)
	}
	if found, err = p.Find(ctx, "imdb", "tt0000042"); err != nil || !reflect.DeepEqual(found, Found{Movies: []string{"42"}}) {
		t.Fatalf("movie lookup: %+v %v", found, err)
	}
	if _, err = p.Find(ctx, "tvdb", "1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("empty answer must be not found", err)
	}
	if _, err = p.Find(ctx, "tmdb", "1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unsupported source accepted", err)
	}
}
