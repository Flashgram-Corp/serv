-- The live rates of the currencies the wallet and the shop quote in.
--
-- The rates are fetched from outside the server and stored, so a client that
-- asks for them gets the last quote the server saw, and a server that cannot
-- reach the sources still has the numbers it had. The operator's own price for
-- one gram lives in shop_settings and wins over the market price; this table
-- is what the market said.
CREATE TABLE IF NOT EXISTS public.market_rates (
    currency      text PRIMARY KEY,
    rate          double precision NOT NULL,
    source        text NOT NULL DEFAULT '',
    updated_at    timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT market_rates_rate_check CHECK (rate > 0)
);

CREATE INDEX IF NOT EXISTS market_rates_updated_idx ON public.market_rates (updated_at DESC);
