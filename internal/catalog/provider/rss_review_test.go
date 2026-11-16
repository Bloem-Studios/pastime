package provider

import "testing"

func TestEpisodeIdentityIncludesFeed(t *testing.T) {
	if RSSEpisodeID("https://synthetic.invalid/a", "episode-1") == RSSEpisodeID("https://synthetic.invalid/b", "episode-1") {
		t.Fatal("episode identity collides across feeds")
	}
}
