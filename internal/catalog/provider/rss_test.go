package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRSSFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel>
<title>Synthetic Signals</title><description>&lt;p&gt;A show about &lt;b&gt;signals&lt;/b&gt;.&lt;/p&gt;</description>
<itunes:author>Sam Host</itunes:author><itunes:image href="https://images.example/show.jpg"/><itunes:category text="Technology"/>
<item><title>Second</title><guid>ep-2</guid><pubDate>Tue, 03 Feb 2026 08:00:00 +0000</pubDate><itunes:duration>1:02:03</itunes:duration><itunes:episode>2</itunes:episode></item>
<item><title>First</title><guid>ep-1</guid><pubDate>Mon, 02 Feb 2026 08:00:00 GMT</pubDate><itunes:duration>1800</itunes:duration></item>
<item><title>First</title><guid>ep-1</guid></item>
</channel></rss>`))
	}))
	defer srv.Close()
	p := RSS{HTTP: srv.Client(), Agent: "test"}
	r, err := p.Fetch(context.Background(), "podcast", srv.URL+"/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Synthetic Signals" || r.Overview != "A show about signals." || r.ImageURL != "https://images.example/show.jpg" || len(r.Credits) != 1 || len(r.Children) != 2 || *r.Total != 2 {
		t.Fatalf("show %+v", r)
	}
	second := r.Children[0]
	if *second.RuntimeMinutes != 63 || *second.EpisodeNumber != 2 || second.ReleaseDate.Day() != 3 || second.ExternalIDs["guid"] != "ep-2" {
		t.Fatalf("episode %+v", second)
	}
	again, _ := p.Fetch(context.Background(), "podcast", srv.URL+"/feed.xml")
	if again.Children[1].ProviderID != r.Children[1].ProviderID {
		t.Fatal("episode ids are not stable")
	}
	if FeedURL("ftp://example.com/feed") || FeedURL("not a url") || !FeedURL("https://example.com/feed.xml") {
		t.Fatal("feed address check")
	}
	if found, _ := p.Search(context.Background(), "podcast", "plain words"); len(found) != 0 {
		t.Fatal("non-address searched")
	}
}
