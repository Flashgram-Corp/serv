-- Renumbered above the production schema_migrations ledger (20261021020000) and made
-- idempotent: a database that already applied the original version of this
-- migration keeps the same schema, and this file is a no-op there.
DROP TABLE IF EXISTS public.user_featured_stickers_read;
ALTER TABLE public.user_sticker_collections DROP CONSTRAINT user_sticker_collections_kind_check;
ALTER TABLE public.user_sticker_collections ADD CONSTRAINT user_sticker_collections_kind_check CHECK ((kind = ANY (ARRAY['faved'::text, 'recent'::text, 'recent_attached'::text, 'gif'::text])));
