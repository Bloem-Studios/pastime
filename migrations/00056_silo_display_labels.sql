-- +goose Up
ALTER TABLE bloem_instances ALTER COLUMN handoff_label SET DEFAULT 'Play in Silo';
UPDATE bloem_instances SET handoff_label='Play in Silo' WHERE handoff_label='Play in Bloem';

-- +goose Down
ALTER TABLE bloem_instances ALTER COLUMN handoff_label SET DEFAULT 'Play in Bloem';
