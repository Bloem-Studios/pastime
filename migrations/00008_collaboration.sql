-- +goose Up
-- Collaborative lists: several people curate one list of catalogued titles. Items are
-- shared catalog records, so each member sees their own tracking state alongside.

CREATE TABLE collab_lists (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 100),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    version integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE collab_members (
    list_id bigint NOT NULL REFERENCES collab_lists(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner','editor','viewer')),
    added_by bigint REFERENCES users(id) ON DELETE SET NULL,
    added_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (list_id, user_id)
);
CREATE INDEX collab_members_user ON collab_members(user_id, list_id);
CREATE UNIQUE INDEX collab_members_one_owner ON collab_members(list_id) WHERE role = 'owner';
CREATE TABLE collab_items (
    list_id bigint NOT NULL REFERENCES collab_lists(id) ON DELETE CASCADE,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    added_by bigint REFERENCES users(id) ON DELETE SET NULL,
    note text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
    position integer NOT NULL DEFAULT 0,
    added_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (list_id, catalog_item_id)
);
CREATE INDEX collab_items_order ON collab_items(list_id, position, added_at);
