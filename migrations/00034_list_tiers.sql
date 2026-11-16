-- +goose Up
-- Tier boards: a manual list can sort its members into ordered, labelled tiers. A member
-- outside every tier is unranked. Tier order inside a tier is separate from the list's
-- own manual order, so arranging a board never reorders the list.
CREATE TABLE list_tiers (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL,
    list_id bigint NOT NULL,
    position integer NOT NULL CHECK (position BETWEEN 1 AND 20),
    label text NOT NULL CHECK (length(label) BETWEEN 1 AND 40),
    color text CHECK (color ~ '^#[0-9a-f]{6}$'),
    UNIQUE (list_id, position),
    UNIQUE (list_id, id),
    FOREIGN KEY (user_id, list_id) REFERENCES personal_lists(user_id, id) ON DELETE CASCADE
);
ALTER TABLE list_entries
    ADD COLUMN tier_id bigint,
    ADD COLUMN tier_position integer,
    ADD CONSTRAINT list_entries_tier FOREIGN KEY (list_id, tier_id) REFERENCES list_tiers(list_id, id) ON DELETE SET NULL (tier_id);

-- +goose Down
ALTER TABLE list_entries DROP CONSTRAINT list_entries_tier, DROP COLUMN tier_position, DROP COLUMN tier_id;
DROP TABLE list_tiers;
