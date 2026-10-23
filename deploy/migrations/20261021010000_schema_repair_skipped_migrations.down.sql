-- Undo the schema repair: restore the pre-repair column names and drop the
-- star gift listing suspension column.
--
-- The renames are reversed only when the repaired spelling is the one present,
-- so a rollback on a database that never had the legacy shape is a no-op for
-- those tables.

-- 0159: private no-forward requests
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_attribute
         WHERE attrelid = 'public.private_no_forwards_requests'::regclass
           AND attname = 'private_message_sender_user_id' AND attnum > 0 AND NOT attisdropped
    ) AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
         WHERE attrelid = 'public.private_no_forwards_requests'::regclass
           AND attname = 'legacy_private_message_sender_user_id' AND attnum > 0 AND NOT attisdropped
    ) THEN
        ALTER TABLE public.private_no_forwards_requests
            RENAME COLUMN private_message_sender_user_id TO legacy_private_message_sender_user_id;
    END IF;
END;
$$;

-- 0154: verification application audit trail
DO $$
DECLARE
    renames text[][] := ARRAY[
        ARRAY['kind',        'legacy_kind'],
        ARRAY['from_status', 'old_status'],
        ARRAY['to_status',   'new_status'],
        ARRAY['actor',       'actor_id'],
        ARRAY['note',        'internal_note']
    ];
    pair text[];
BEGIN
    FOREACH pair SLICE 1 IN ARRAY renames LOOP
        IF EXISTS (
            SELECT 1 FROM pg_attribute
             WHERE attrelid = 'public.verification_application_events'::regclass
               AND attname = pair[1] AND attnum > 0 AND NOT attisdropped
        ) AND NOT EXISTS (
            SELECT 1 FROM pg_attribute
             WHERE attrelid = 'public.verification_application_events'::regclass
               AND attname = pair[2] AND attnum > 0 AND NOT attisdropped
        ) THEN
            EXECUTE format(
                'ALTER TABLE public.verification_application_events RENAME COLUMN %I TO %I',
                pair[1], pair[2]);
        END IF;
    END LOOP;
END;
$$;

-- 0211: star gift listing suspension
DROP INDEX IF EXISTS public.star_gift_listings_seller_suspension_idx;
ALTER TABLE public.star_gift_listings DROP COLUMN IF EXISTS suspended;
