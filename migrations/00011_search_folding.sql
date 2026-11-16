-- +goose Up
-- Search ignores accents and case, and tolerates typos in a person's own library.
-- search_fold is the one normalization used by both the stored documents and the
-- queries, so "Amelie" finds "Amélie" and "Pokemon" finds "Pokémon". Like the
-- extensions it lives in public, shared by every schema in the database, so schemas
-- migrating at the same time take one lock and create it once.
SELECT pg_advisory_xact_lock(hashtext('pastime.search_fold'));
CREATE EXTENSION IF NOT EXISTS unaccent SCHEMA public;
CREATE EXTENSION IF NOT EXISTS btree_gin SCHEMA public;

-- +goose StatementBegin
DO $$
BEGIN
    IF to_regprocedure('public.search_fold(text)') IS NULL THEN
        CREATE FUNCTION public.search_fold(text) RETURNS text
            LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
            RETURN lower(public.unaccent('public.unaccent'::regdictionary, $1));
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE catalog_items DROP COLUMN search_document;
ALTER TABLE catalog_items ADD COLUMN search_document tsvector GENERATED ALWAYS AS
    (to_tsvector('simple'::regconfig, public.search_fold(title || ' ' || original_title))) STORED;
CREATE INDEX catalog_items_search ON catalog_items USING gin (search_document) WHERE parent_id IS NULL;
DROP INDEX catalog_items_title_trgm;
CREATE INDEX catalog_items_title_trgm ON catalog_items
    USING gist (kind, public.search_fold(title) public.gist_trgm_ops) WHERE parent_id IS NULL;

-- Library indexes lead with the account, so a search reads only that person's titles
-- however many accounts share the table.
ALTER TABLE library_entries DROP COLUMN search_document;
ALTER TABLE library_entries ADD COLUMN search_document tsvector GENERATED ALWAYS AS
    (to_tsvector('simple'::regconfig, public.search_fold(title))) STORED;
CREATE INDEX library_entries_search ON library_entries USING gin (user_id, search_document);
CREATE INDEX library_entries_title_trgm ON library_entries
    USING gin (user_id, public.search_fold(title) public.gin_trgm_ops);

-- Dropping and re-adding the columns rewrites both tables, so refresh the planner's
-- statistics before anything queries them.
ANALYZE catalog_items;
ANALYZE library_entries;
