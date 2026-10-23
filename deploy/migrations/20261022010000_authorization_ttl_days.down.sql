-- Renumbered above the production schema_migrations ledger (20261021020000) and made
-- idempotent: a database that already applied the original version of this
-- migration keeps the same schema, and this file is a no-op there.
ALTER TABLE public.account_settings
    DROP COLUMN authorization_ttl_days;
