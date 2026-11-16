package transfer

import (
	"bytes"
	"context"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestManualSnapshotRoundTripAndCompleteness(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	entry, err := s.Create(ctx, f.Owner, testkit.Key(t, f), library.EntryInput{Title: "Synthetic manual book", MediaType: "book", Unit: "pages", Status: "planned", Rating: func() *int { v := 7; return &v }(), Notes: "Private notes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE library_entries SET cycle=3,completed_cycles=2,progress=7 WHERE id=$1", entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, `INSERT INTO tags(user_id,name) VALUES($1,'Synthetic tag')`, f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, `INSERT INTO entry_tags(user_id,entry_id,tag_id) SELECT $1,$2,id FROM tags WHERE user_id=$1`, f.Owner.User.ID, entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, `INSERT INTO owned_copies(user_id,entry_id,format,currency,price_cents,notes) VALUES($1,$2,'paperback','USD',1234,'Copy note')`, f.Owner.User.ID, entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveView(ctx, f.Owner, 0, library.SavedViewInput{Name: "Snapshot view", Sort: "added", Layout: "grid"}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if n, err := Write(ctx, f.Pool, f.Owner, &b); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	other := f.User(t, "restore_target", "member")
	resolve := func(context.Context, Reference) (int64, error) {
		t.Fatal("manual title unexpectedly needs catalog")
		return 0, nil
	}
	broken := b.Bytes()[:bytes.LastIndex(bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), []byte{'\n'})+1]
	if _, _, err = Restore(ctx, f.Pool, other.User.ID, "", broken, resolve); err == nil {
		t.Fatal("accepted truncated snapshot")
	}
	if n, _, err := Restore(ctx, f.Pool, other.User.ID, "", b.Bytes(), resolve); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var evidence bool
	if err = f.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM saved_views WHERE user_id=$1)=1 AND (SELECT count(*) FROM entry_status_history WHERE user_id=$1)>0 AND (SELECT scored_at FROM library_entries WHERE user_id=$1)=(SELECT scored_at FROM library_entries WHERE user_id=$2)`, other.User.ID, f.Owner.User.ID).Scan(&evidence); err != nil || !evidence {
		t.Fatal("snapshot lost personal evidence", err)
	}
	var notes string
	var cycle, progress, copies, tags int
	if err = f.Pool.QueryRow(ctx, `SELECT notes,cycle,progress,(SELECT count(*) FROM owned_copies WHERE user_id=$1),(SELECT count(*) FROM entry_tags WHERE user_id=$1) FROM library_entries WHERE user_id=$1`, other.User.ID).Scan(&notes, &cycle, &progress, &copies, &tags); err != nil {
		t.Fatal(err)
	}
	if notes != "Private notes" || cycle != 3 || progress != 7 || copies != 1 || tags != 1 {
		t.Fatal(notes, cycle, progress, copies, tags)
	}
	if n, _, err := Restore(ctx, f.Pool, other.User.ID, "", b.Bytes(), resolve); err != nil || n != 0 {
		t.Fatal("replay", n, err)
	}
}

func TestSnapshotPreservesHistoricalPlayEvidenceAndLists(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	var catalogID, entryID, fieldID int64
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(provider,provider_id,kind,title,unit,runtime_minutes) VALUES('synthetic','history','movie','History','minutes',90) RETURNING id`).Scan(&catalogID); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(ctx, `INSERT INTO library_entries(user_id,catalog_item_id,title,media_type,unit,status,cycle,completed_cycles,edition) VALUES($1,$2,'History','movie','minutes','completed',3,3,'{"title":"Selected edition","pages":300}') RETURNING id`, f.Owner.User.ID, catalogID).Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `INSERT INTO plays(user_id,entry_id,catalog_item_id,cycle,played_at,media_type,source,external_id,unmapped) VALUES($1,$2,$3,2,'2020-02-02T12:00:00Z','movie','native','old-identity',true)`, f.Owner.User.ID, entryID, catalogID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, "UPDATE catalog_items SET runtime_minutes=120 WHERE id=$1", catalogID); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(ctx, `INSERT INTO collection_fields(user_id,name,kind,choices) VALUES($1,'Condition','choice',ARRAY['Good','New']) RETURNING id`, f.Owner.User.ID).Scan(&fieldID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `INSERT INTO owned_copies(user_id,entry_id,format,custom) VALUES($1,$2,'dvd',jsonb_build_object($3::text,'Good'))`, f.Owner.User.ID, entryID, fmt.Sprint(fieldID)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `WITH l AS(INSERT INTO personal_lists(user_id,name,visibility,slug) VALUES($1,'Snapshot list','public','snapshot-list') RETURNING id) INSERT INTO list_entries(user_id,list_id,entry_id,position,note) SELECT $1,id,$2,7,'Membership note' FROM l`, f.Owner.User.ID, entryID); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if _, err := Write(ctx, f.Pool, f.Owner, &b); err != nil {
		t.Fatal(err)
	}
	other := f.User(t, "history_restore", "member")
	resolve := func(_ context.Context, r Reference) (int64, error) {
		if r.ProviderID != "history" {
			t.Fatal(r)
		}
		return catalogID, nil
	}
	if n, p, err := Restore(ctx, f.Pool, other.User.ID, "", b.Bytes(), resolve); err != nil || n != 1 || p != 1 {
		t.Fatal(n, p, err)
	}
	var cycle, minutes int
	var unmapped bool
	if err := f.Pool.QueryRow(ctx, "SELECT cycle,minutes,unmapped FROM plays WHERE user_id=$1", other.User.ID).Scan(&cycle, &minutes, &unmapped); err != nil {
		t.Fatal(err)
	}
	if cycle != 2 || minutes != 90 || !unmapped {
		t.Fatal("history changed", cycle, minutes, unmapped)
	}
	var visibility, note, value string
	if err := f.Pool.QueryRow(ctx, "SELECT l.visibility,e.note FROM personal_lists l JOIN list_entries e ON e.list_id=l.id WHERE l.user_id=$1", other.User.ID).Scan(&visibility, &note); err != nil {
		t.Fatal(err)
	}
	if visibility != "private" || note != "Membership note" {
		t.Fatal(visibility, note)
	}
	if err := f.Pool.QueryRow(ctx, "SELECT c.custom->>f.id::text FROM owned_copies c JOIN collection_fields f ON f.user_id=c.user_id WHERE c.user_id=$1", other.User.ID).Scan(&value); err != nil || value != "Good" {
		t.Fatal(value, err)
	}
	// A second distinct snapshot cannot overwrite subsequent user changes.
	if _, err := f.Pool.Exec(ctx, "UPDATE library_entries SET notes='new source note' WHERE id=$1", entryID); err != nil {
		t.Fatal(err)
	}
	var newer bytes.Buffer
	if _, err := Write(ctx, f.Pool, f.Owner, &newer); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Restore(ctx, f.Pool, other.User.ID, "", newer.Bytes(), resolve); err == nil {
		t.Fatal("overwrote nonempty target")
	}
}

func TestNativeExportRequiresListReadScope(t *testing.T) {
	f := testkit.New(t)
	a := f.Owner
	a.Kind = "token"
	a.Scopes = []string{"library:read"}
	var b bytes.Buffer
	if _, err := Write(context.Background(), f.Pool, a, &b); err == nil || b.Len() != 0 {
		t.Fatal("library-only token exported private lists", err)
	}
}

func TestNativeRestoreCommitsOutcomeWithData(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	if _, err := s.Create(ctx, f.Owner, testkit.Key(t, f), library.EntryInput{Title: "Atomic restore", MediaType: "book", Unit: "pages", Status: "planned"}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if _, err := Write(ctx, f.Pool, f.Owner, &b); err != nil {
		t.Fatal(err)
	}
	target := f.User(t, "atomic_restore", "member")
	var run string
	if err := f.Pool.QueryRow(ctx, `INSERT INTO import_runs(user_id,source,status,upload_name,upload) VALUES($1,'native','running','snapshot', $2) RETURNING id::text`, target.User.ID, b.Bytes()).Scan(&run); err != nil {
		t.Fatal(err)
	}
	resolve := func(context.Context, Reference) (int64, error) { t.Fatal("unexpected reference"); return 0, nil }
	if _, err := f.Pool.Exec(ctx, `CREATE FUNCTION reject_outcome() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='succeeded' THEN RAISE EXCEPTION 'synthetic outcome failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_outcome BEFORE UPDATE ON import_runs FOR EACH ROW EXECUTE FUNCTION reject_outcome()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Restore(ctx, f.Pool, target.User.ID, run, b.Bytes(), resolve); err == nil {
		t.Fatal("restore committed without durable outcome")
	}
	var entries, receipts int
	if err := f.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM library_entries WHERE user_id=$1),(SELECT count(*) FROM native_restore_receipts WHERE user_id=$1)`, target.User.ID).Scan(&entries, &receipts); err != nil {
		t.Fatal(err)
	}
	if entries != 0 || receipts != 0 {
		t.Fatal("partial commit", entries, receipts)
	}
	if _, err := f.Pool.Exec(ctx, `DROP TRIGGER reject_outcome ON import_runs`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Restore(ctx, f.Pool, target.User.ID, run, b.Bytes(), resolve); err != nil {
		t.Fatal(err)
	}
	var status string
	var imported, total int
	var cleared bool
	if err := f.Pool.QueryRow(ctx, `SELECT status,imported,total,upload IS NULL FROM import_runs WHERE id=$1`, run).Scan(&status, &imported, &total, &cleared); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || imported != 1 || total != 1 || !cleared {
		t.Fatal(status, imported, total, cleared)
	}
}
