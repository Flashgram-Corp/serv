-- The agentbot's bootstrap used to create the two indexes on public.private_messages
-- itself. That made the bot the owner of an index on the server's table, and a
-- bot that runs under a service role without ownership could not even re-run its
-- own idempotent bootstrap: "must be owner of table private_messages" is raised
-- by CREATE INDEX IF NOT EXISTS even when the index is already there.
--
-- An index on a table belongs to the schema that owns the table, so it is
-- created here. The bot keeps its own schema's tables, which it owns.
CREATE INDEX IF NOT EXISTS agent_pm_recipient_idx ON public.private_messages (recipient_user_id, id);
CREATE INDEX IF NOT EXISTS agent_pm_sender_idx ON public.private_messages (sender_user_id, id);
