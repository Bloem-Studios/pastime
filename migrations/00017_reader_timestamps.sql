-- +goose Up
ALTER TABLE kosync_documents ADD COLUMN source_timestamp bigint NOT NULL DEFAULT 0;
