-- +goose Up
CREATE TABLE saved_views (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 rules jsonb NOT NULL DEFAULT '{}',
 sort text NOT NULL CHECK(sort IN ('added','activity','title','rating','year')),
 layout text NOT NULL CHECK(layout IN ('grid','list')),
 position integer NOT NULL DEFAULT 0,
 version integer NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(user_id,name)
);
CREATE INDEX saved_views_owner ON saved_views(user_id,position,id);
