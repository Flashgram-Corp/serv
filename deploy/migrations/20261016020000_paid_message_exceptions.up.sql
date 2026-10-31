CREATE TABLE public.paid_message_exceptions (
    scope_channel_id bigint DEFAULT 0 NOT NULL,
    granter_user_id bigint DEFAULT 0 NOT NULL,
    user_id bigint NOT NULL,
    granted_by bigint DEFAULT 0 NOT NULL,
    created_at integer DEFAULT 0 NOT NULL,
    updated_at integer DEFAULT 0 NOT NULL,
    revenue bigint DEFAULT 0 NOT NULL,
    refunded_stars bigint DEFAULT 0 NOT NULL,
    CONSTRAINT paid_message_exceptions_pkey PRIMARY KEY (scope_channel_id, granter_user_id, user_id)
);
