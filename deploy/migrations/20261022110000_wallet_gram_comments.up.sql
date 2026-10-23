-- The comment of a wallet movement: the note the sender writes on a transfer.
--
-- The ledger kept the reason and the counterparty but not the note, so a
-- transfer arrived on the other side as a bare number. The client's wallet
-- history has a field for it (wallet.getTransactions), and it is what makes a
-- transfer readable months later.
ALTER TABLE public.ton_transactions
    ADD COLUMN IF NOT EXISTS comment text NOT NULL DEFAULT '';
