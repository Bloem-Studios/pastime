-- +goose Up
ALTER TABLE bloem_instances
 ADD COLUMN handoff_enabled boolean NOT NULL DEFAULT true,
 ADD COLUMN handoff_label text NOT NULL DEFAULT 'Play in Bloem' CHECK (length(handoff_label) BETWEEN 1 AND 100),
 ADD COLUMN handoff_base_url text;

-- +goose Down
ALTER TABLE bloem_instances DROP COLUMN handoff_base_url, DROP COLUMN handoff_label, DROP COLUMN handoff_enabled;
