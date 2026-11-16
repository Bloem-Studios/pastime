-- The library's "Last played" order: a saved view may keep it, and each entry's latest real
-- play (bulk marks excluded) is read from an index instead of scanning the entry's plays.
-- +goose Up
ALTER TABLE saved_views DROP CONSTRAINT saved_views_sort_check;
ALTER TABLE saved_views ADD CONSTRAINT saved_views_sort_check CHECK (sort IN ('added', 'activity', 'title', 'rating', 'year', 'played'));
CREATE INDEX plays_latest_real ON plays (profile_id, entry_id, played_at DESC) WHERE NOT marked;

-- +goose Down
DROP INDEX plays_latest_real;
UPDATE saved_views SET sort = 'activity' WHERE sort = 'played';
ALTER TABLE saved_views DROP CONSTRAINT saved_views_sort_check;
ALTER TABLE saved_views ADD CONSTRAINT saved_views_sort_check CHECK (sort IN ('added', 'activity', 'title', 'rating', 'year'));
