package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Only the explicitly selected subprocess fixture registers this controller.
// All list and tracking mutations use Pastime's own fresh-authority services.
func serveListAcceptanceFixture(s *Server, f *testkit.Fixture, cat *catalog.Service, actor func(context.Context, string) (identity.Actor, error)) {
	s.Router.Post("/fixture/list", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Profile string
			ListID  int64
			IDs     []int
			Delete  bool
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "fixture input", 400)
			return
		}
		a, err := actor(r.Context(), in.Profile)
		if err != nil {
			http.Error(w, "fixture actor", 403)
			return
		}
		store := library.Store{Pool: f.Pool}
		id, version := in.ListID, 0
		if id == 0 {
			result, e := store.SaveList(r.Context(), a, 0, listFixtureUUID(), library.ListInput{Name: "Synthetic source list", Kind: "manual"})
			err = e
			id, version = result.ID, result.Version
		} else {
			list, e := store.List(r.Context(), a, id)
			err = e
			version = list.Version
		}
		if err != nil {
			http.Error(w, "fixture source: "+err.Error(), 500)
			return
		}
		if in.Delete {
			_, err = store.DeleteList(r.Context(), a, id, listFixtureUUID(), version)
			if err != nil {
				http.Error(w, "fixture delete", 500)
				return
			}
			respond(w, 200, map[string]any{"deleted": true})
			return
		}
		rows, err := f.Pool.Query(r.Context(), `SELECT entry_id FROM list_entries WHERE list_id=$1 ORDER BY position,entry_id`, id)
		if err != nil {
			http.Error(w, "fixture membership", 500)
			return
		}
		existing := []int64{}
		for rows.Next() {
			var entry int64
			if err = rows.Scan(&entry); err != nil {
				break
			}
			existing = append(existing, entry)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			http.Error(w, "fixture membership", 500)
			return
		}
		for _, entry := range existing {
			result, e := store.Membership(r.Context(), a, id, entry, listFixtureUUID(), version, false)
			if e != nil {
				http.Error(w, "fixture removal", 500)
				return
			}
			version = result.Version
		}
		for _, external := range in.IDs {
			title, e := cat.Resolve(r.Context(), "tmdb", "movie", strconv.Itoa(external))
			if e != nil {
				http.Error(w, "fixture title", 500)
				return
			}
			// The shared compatibility provider emits a deliberately abbreviated
			// IMDb fixture ID. List export requires canonical external identities.
			if _, e = f.Pool.Exec(r.Context(), `UPDATE catalog_items SET external_ids='{}'::jsonb WHERE id=$1`, title.ID); e != nil {
				http.Error(w, "fixture identity", 500)
				return
			}
			entry, e := store.EntryForCatalog(r.Context(), a, title.ID)
			if e != nil {
				http.Error(w, "fixture entry", 500)
				return
			}
			var entryID int64
			if entry == nil {
				result, e := store.Track(r.Context(), a, listFixtureUUID(), library.TrackInput{CatalogID: title.ID, Status: "planned"})
				if e != nil {
					http.Error(w, "fixture track", 500)
					return
				}
				entryID = result.ID
			} else {
				entryID = entry.ID
			}
			result, e := store.Membership(r.Context(), a, id, entryID, listFixtureUUID(), version, true)
			if e != nil {
				http.Error(w, "fixture addition", 500)
				return
			}
			version = result.Version
		}
		respond(w, 200, map[string]any{"list_id": id, "url": "http://127.0.0.1:8080/lists/" + strconv.FormatInt(id, 10)})
	})
}

func listFixtureUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	raw[6] = raw[6]&15 | 64
	raw[8] = raw[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
}
