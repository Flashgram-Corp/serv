-- A replayed transfer has to return the same result as the first attempt, and
-- the result includes the ledger row the service message points at. The
-- command row kept the balance but not the row id, so a replay answered with
-- a balance and no transaction.
ALTER TABLE public.wallet_gram_commands
    ADD COLUMN IF NOT EXISTS transaction_id bigint NOT NULL DEFAULT 0;
