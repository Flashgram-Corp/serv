-- Renumbered above the production schema_migrations ledger (20261021020000) and made
-- idempotent: a database that already applied the original version of this
-- migration keeps the same schema, and this file is a no-op there.
-- Saved ringtones: per-user references to audio documents. The audio bytes
-- live in documents/blobs (content-addressed, shared); this table only maps
-- ownership. Eviction of the oldest row past the cap happens in the store
-- layer, never silently in a migration.
CREATE TABLE IF NOT EXISTS public.user_saved_ringtones (
    user_id bigint NOT NULL,
    document_id bigint NOT NULL,
    created_at integer DEFAULT 0 NOT NULL,
    CONSTRAINT user_saved_ringtones_pkey PRIMARY KEY (user_id, document_id),
    CONSTRAINT user_saved_ringtones_check CHECK (user_id > 0 AND document_id > 0)
);
CREATE INDEX IF NOT EXISTS user_saved_ringtones_user_created_idx
    ON public.user_saved_ringtones USING btree (user_id, created_at DESC, document_id DESC);
