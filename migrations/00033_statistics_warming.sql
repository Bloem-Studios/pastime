-- +goose Up
CREATE TABLE maintenance_cursors(name text PRIMARY KEY,last_id bigint NOT NULL DEFAULT 0);
INSERT INTO maintenance_cursors(name) VALUES('statistics_warm');
