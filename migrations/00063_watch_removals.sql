-- Titles a person removed from a profile's library while the profile was connected to
-- Bloem. state:read serves each as a watched removal on that profile's connection, and
-- Bloem removes that profile's watch history of the title up to removed_at, keeping
-- watches made later. A play of the title after removed_at retires the note.
-- +goose Up
CREATE TABLE watch_removals (
    profile_id bigint NOT NULL REFERENCES tracking_profiles(id) ON DELETE CASCADE,
    catalog_item_id bigint NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    removed_at timestamptz NOT NULL,
    PRIMARY KEY (profile_id, catalog_item_id)
);

-- +goose Down
DROP TABLE watch_removals;
