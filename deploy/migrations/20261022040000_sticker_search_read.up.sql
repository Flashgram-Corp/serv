-- Renumbered above the production schema_migrations ledger (20261021020000) and made
-- idempotent: a database that already applied the original version of this
-- migration keeps the same schema, and this file is a no-op there.
-- Sticker discovery: per-user featured read markers + emoji-status recents kind.
-- user_featured_stickers_read records messages.readFeaturedStickers calls
-- (idempotent PK): featuredStickersForKind populates Unread from the complement.
-- user_sticker_collections gains the recent_emoji_status kind reusing the
-- existing recents machinery (used_at ordering, cap, clear).
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'public.user_sticker_collections'::regclass
           AND conname = 'user_sticker_collections_kind_check'
    ) THEN
        ALTER TABLE public.user_sticker_collections
            DROP CONSTRAINT user_sticker_collections_kind_check;
    END IF;
    ALTER TABLE public.user_sticker_collections
        ADD CONSTRAINT user_sticker_collections_kind_check
        CHECK ((kind = ANY (ARRAY['faved'::text, 'recent'::text, 'recent_attached'::text, 'gif'::text, 'recent_emoji_status'::text])));
END;
$$;

CREATE TABLE IF NOT EXISTS public.user_featured_stickers_read (
    user_id bigint NOT NULL,
    sticker_set_id bigint NOT NULL,
    read_at integer DEFAULT 0 NOT NULL,
    CONSTRAINT user_featured_stickers_read_pkey PRIMARY KEY (user_id, sticker_set_id),
    CONSTRAINT user_featured_stickers_read_check CHECK (user_id>0 AND sticker_set_id>0)
);
