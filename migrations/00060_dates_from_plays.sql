-- Entries that plays completed or started were dated when the plays were applied, so
-- synced and imported history carried the sync's date instead of the plays' own. Date
-- them from the plays of their current cycle: an entry starts no later than its first
-- play, and an entry completed by plays finishes at its latest one. A completion a
-- person set by hand (adding, editing or moving the progress of an entry) keeps its
-- date. Corrected dates cannot be returned to the old ones, so there is no Down.
-- +goose Up
WITH dated AS (
    SELECT p.profile_id, p.entry_id, min(p.played_at) AS first_play, LEAST(max(p.played_at), clock_timestamp()) AS last_play
    FROM plays p JOIN library_entries e ON e.profile_id = p.profile_id AND e.id = p.entry_id AND p.cycle = e.cycle
    WHERE NOT p.unmapped
    GROUP BY 1, 2
), decided AS (
    SELECT e.profile_id, e.id,
        CASE WHEN e.status = 'completed' AND e.finished_at > d.last_play
            AND NOT COALESCE((SELECT h.source ~ '^user:entry\.(track|create|progress/|replace/)' FROM entry_status_history h WHERE h.profile_id = e.profile_id AND h.entry_id = e.id ORDER BY h.id DESC LIMIT 1), false)
            THEN d.last_play ELSE e.finished_at END AS finished_at,
        CASE WHEN e.started_at > d.first_play THEN d.first_play ELSE e.started_at END AS started_at
    FROM library_entries e JOIN dated d ON d.profile_id = e.profile_id AND d.entry_id = e.id
)
UPDATE library_entries e
SET finished_at = x.finished_at, started_at = x.started_at, version = e.version + 1, updated_at = clock_timestamp()
FROM decided x
WHERE x.profile_id = e.profile_id AND x.id = e.id
  AND (x.finished_at IS DISTINCT FROM e.finished_at OR x.started_at IS DISTINCT FROM e.started_at);
