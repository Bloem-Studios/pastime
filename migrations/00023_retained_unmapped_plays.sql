-- +goose Up
ALTER TABLE plays ADD COLUMN unmapped boolean NOT NULL DEFAULT false;
-- +goose Down
ALTER TABLE plays DROP COLUMN unmapped;
