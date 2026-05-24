-- F#2 (round-3) — intentional no-op down migration.
--
-- The 000017 up migration backfills allow_insecure_tls=true on existing
-- CA-less remote rows. Reversing that backfill is semantically impossible:
-- once an operator has viewed, audited, or explicitly flipped some of the
-- auto-flagged rows, there is no record of which rows were originally
-- flagged by the backfill vs which were user-set. A reversal would either
-- (a) flip every CA-less row back to false (losing the user's audit
-- decisions and immediately breaking every remote-cluster connection that
-- depends on the explicit opt-in), or (b) be a no-op that defers the
-- problem to 000016's down migration.
--
-- (b) is the only safe choice. The actual destructive operation lives in
-- 000016.down.sql; this file exists so golang-migrate has a .down for
-- 000017 and so an operator running `migrate down 1` from 000017 does
-- nothing surprising.
--
-- For the full rollback procedure, see 000016_cluster_allow_insecure_tls.down.sql.

SELECT 1 WHERE false;
