-- +goose Up
ALTER TABLE bloem_availability ADD COLUMN pending boolean NOT NULL DEFAULT true;
UPDATE bloem_availability SET pending=false WHERE checked_at IS NOT NULL;
DROP INDEX bloem_availability_pending;
CREATE INDEX bloem_availability_pending ON bloem_availability(instance_id,requested_at) WHERE pending;
-- +goose Down
DROP INDEX bloem_availability_pending;
ALTER TABLE bloem_availability DROP COLUMN pending;
CREATE INDEX bloem_availability_pending ON bloem_availability(instance_id,requested_at) WHERE checked_at IS NULL;
