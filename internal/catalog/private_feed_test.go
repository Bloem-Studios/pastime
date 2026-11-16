package catalog

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestOnlyDirectoryPublishedFeedsEnterSharedCatalog(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Service{Pool: f.Pool}
	private := "https://example.test/secret-in-path/feed.xml?token=synthetic"
	if err := s.publicFeed(ctx, private); err == nil {
		t.Fatal("unverified credential-bearing feed accepted")
	}
	if _, err := f.Pool.Exec(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,external_ids) VALUES('itunes','123','podcast','Public synthetic podcast','episodes','{"feed":"https://example.test/public.xml"}')`); err != nil {
		t.Fatal(err)
	}
	if err := s.publicFeed(ctx, "https://example.test/public.xml"); err != nil {
		t.Fatal(err)
	}
	if err := s.publicFeed(ctx, "https://example.test/public.xml?token=synthetic"); err == nil {
		t.Fatal("directory validation allowed changed URL")
	}
}
