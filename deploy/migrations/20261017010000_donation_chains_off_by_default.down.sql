-- Re-enable the rows this migration cleared, but only the ones that are still
-- carrying the seeded placeholder endpoint -- i.e. restore the pre-migration
-- state exactly, without overriding an operator who has since filled in a real
-- endpoint or flipped a chain deliberately.
UPDATE public.donation_chains
   SET enabled = true
 WHERE enabled = false
   AND rpc_url IN ('', 'http://127.0.0.1:7545');