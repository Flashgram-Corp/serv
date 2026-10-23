-- Renumbered above the production schema_migrations ledger (20261021020000) and made
-- idempotent: a database that already applied the original version of this
-- migration keeps the same schema, and this file is a no-op there.
-- Admin visual boost: additive subscriber override. participants_count keeps
-- serving the displayed total (real members + boost) so all existing readers
-- are unchanged; refreshChannelCountsTx adds the boost after recounting.
ALTER TABLE public.channels
    ADD COLUMN IF NOT EXISTS participants_count_boost integer NOT NULL DEFAULT 0;
