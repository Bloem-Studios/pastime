-- +goose Up
ALTER TABLE jobs ADD COLUMN payload_version integer NOT NULL DEFAULT 1 CHECK (payload_version>0);
