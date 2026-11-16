-- +goose Up
-- Release A for the plain Silo plugin. Additive only, and written so that applying it
-- again after version 57 was un-recorded (a binary-only rollback) succeeds.
-- The Down keeps every binding, key and receipt; it drops only the self-service marker.

-- A convergent personal-state write (the profile already has that favourite, watchlist
-- entry or rating) is recorded as no_change; the original CHECK rejected it.
ALTER TABLE bloem_event_receipts DROP CONSTRAINT IF EXISTS bloem_event_receipts_result_check;
ALTER TABLE bloem_event_receipts ADD CONSTRAINT bloem_event_receipts_result_check CHECK(result IN ('applied','stale','no_change'));

-- Bindings a person created on the Connect Silo page are self_service. Provisioning
-- snapshots and source account disables from plugin 0.1.14 touch only the bindings
-- that provisioning created.
ALTER TABLE bloem_profiles ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'provisioned' CHECK(origin IN ('provisioned','self_service'));

-- +goose Down
-- Self-service bindings and their keys stay; only the marker that shields them from
-- 0.1.14 provisioning sweeps goes.
ALTER TABLE bloem_profiles DROP COLUMN IF EXISTS origin;
-- Recorded no_change receipts stay and keep replaying; new ones are refused again.
ALTER TABLE bloem_event_receipts DROP CONSTRAINT IF EXISTS bloem_event_receipts_result_check;
ALTER TABLE bloem_event_receipts ADD CONSTRAINT bloem_event_receipts_result_check CHECK(result IN ('applied','stale')) NOT VALID;
