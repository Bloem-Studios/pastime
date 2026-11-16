-- +goose Up
-- Alternate episode orders (DVD, absolute, story arcs...) from the provider. An order
-- regroups the same episode records, so plays and progress do not change; it decides
-- how episodes are grouped and which one is up next.
CREATE TABLE episode_orders (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 100),
    show_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    kind text NOT NULL DEFAULT '' CHECK (length(kind) <= 40),
    refreshed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX episode_orders_show ON episode_orders(show_id);
CREATE TABLE episode_order_items (
    order_id text NOT NULL REFERENCES episode_orders(id) ON DELETE CASCADE,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    group_number integer NOT NULL,
    group_name text NOT NULL CHECK (length(group_name) <= 200),
    position integer NOT NULL,
    PRIMARY KEY (order_id, catalog_item_id)
);
CREATE INDEX episode_order_items_position ON episode_order_items(order_id, position);
ALTER TABLE library_entries ADD COLUMN episode_order text REFERENCES episode_orders(id) ON DELETE SET NULL;
