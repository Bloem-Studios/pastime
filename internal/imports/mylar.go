package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
)

// Mylar's API returns a complete index rather than a paginated index. Refuse
// oversized scans before staging anything; never reconcile an incomplete scan.
func fetchMylar(ctx context.Context, client *http.Client, cfg *LinkConfig) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	get := func(command, id string, out any) error {
		q := url.Values{"apikey": {cfg.Token}, "cmd": {command}}
		if id != "" {
			q.Set("id", id)
		}
		var envelope struct {
			Success bool `json:"success"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
			Data json.RawMessage `json:"data"`
		}
		if _, err := getJSON(ctx, client, "GET", base+"/api?"+q.Encode(), nil, nil, &envelope); err != nil {
			return err
		}
		if !envelope.Success {
			switch envelope.Error.Message {
			case "Incorrect API key", "Missing API key":
				return ErrCredentials
			case "API not enabled":
				return provider.ErrNotConfigured
			}
			return fmt.Errorf("%w: Mylar did not complete the request", ErrFormat)
		}
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, out) != nil {
			return ErrFormat
		}
		return nil
	}
	var index []struct {
		ID   json.Number `json:"id"`
		Name string      `json:"name"`
	}
	if err = get("getIndex", "", &index); err != nil {
		return nil, err
	}
	if len(index) > 2000 {
		return nil, fmt.Errorf("%w: Mylar index exceeds 2000 series", ErrFormat)
	}
	type issue struct {
		ID     json.Number `json:"id"`
		Number string      `json:"number"`
		Status string      `json:"status"`
	}
	out := []Record{}
	seen := map[string]bool{}
	total := 0
	for _, v := range index {
		id := v.ID.String()
		n, e := strconv.ParseInt(id, 10, 64)
		if e != nil || n < 1 || seen[id] {
			return nil, ErrFormat
		}
		seen[id] = true
		var detail struct {
			Issues  []issue `json:"issues"`
			Annuals []issue `json:"annuals"`
		}
		if err = get("getComic", id, &detail); err != nil {
			return nil, err
		}
		if detail.Issues == nil || detail.Annuals == nil {
			return nil, ErrFormat
		}
		r := Record{Label: v.Name, Kind: "comic", Status: "planned", Direct: &DirectRef{Provider: "comicvine", Kind: "comic", ID: id}}
		for group, items := range [][]issue{detail.Issues, detail.Annuals} {
			for _, i := range items {
				if !strings.EqualFold(i.Status, "downloaded") && !strings.EqualFold(i.Status, "archived") {
					continue
				}
				if n, e := strconv.ParseInt(i.ID.String(), 10, 64); e != nil || n < 1 {
					return nil, ErrFormat
				}
				total++
				if total > 100000 || len(r.Copies) >= 10000 {
					return nil, fmt.Errorf("%w: Mylar scan exceeds the copy limit", ErrFormat)
				}
				edition := "#" + i.Number
				kind := "issue"
				if group == 1 {
					edition = "Annual " + edition
					kind = "annual"
				}
				r.Copies = append(r.Copies, library.ImportCopy{Format: "digital", Edition: edition, Store: "Mylar3", ExternalID: id + ":" + kind + ":" + i.ID.String(), ChildID: i.ID.String()})
			}
		}
		if len(r.Copies) > 0 {
			out = append(out, r)
		}
	}
	return out, nil
}

// ownershipSources are linked services whose sync is a complete index of owned
// copies, so a copy missing from a successful sync is no longer owned.
var ownershipSources = []string{"mylar3", "kapowarr"}

// Only a fully applied successful batch may remove source-owned copies. The
// generation fence prevents old credentials/scans from reconciling a new link.
func reconcileOwned(ctx context.Context, tx pgx.Tx, runID string) error {
	var user, link int64
	var raw []byte
	var source string
	err := tx.QueryRow(ctx, `SELECT b.profile_id,b.link_id,b.records,l.kind FROM import_batches b JOIN service_links l ON l.id=b.link_id AND l.generation=b.generation JOIN tracking_profiles tracking ON tracking.id=b.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id AND u.active WHERE b.run_id=$1 AND l.kind=ANY($2) FOR SHARE OF l,u`, runID, ownershipSources).Scan(&user, &link, &raw, &source)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var records []Record
	if err = json.Unmarshal(raw, &records); err != nil {
		return err
	}
	ids := []string{}
	for _, r := range records {
		for _, c := range r.Copies {
			ids = append(ids, c.ExternalID)
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM owned_copies WHERE profile_id=$1 AND source=$3 AND NOT (external_id=ANY($2::text[]))`, user, ids, source)
	return err
}
