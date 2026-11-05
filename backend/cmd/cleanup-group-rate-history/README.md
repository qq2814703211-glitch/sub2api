# Group rate history cleanup

`group_rate_history` is append-only: every real change of `groups.rate_multiplier`
adds one row through a database trigger. The history only feeds the user-facing
"rate changed" arrow and NEW badge; billing and routing never read it.

There is deliberately no background job. Run this maintenance command manually
(or from the operator's existing maintenance scheduler) when the table should be
trimmed. It is a dry run unless `--execute` is supplied and always requires an
explicit RFC3339 cutoff, so rows still inside the 48h display window are not
removed accidentally.

```sh
go run ./cmd/cleanup-group-rate-history --before 2026-08-01T00:00:00Z
go run ./cmd/cleanup-group-rate-history --before 2026-08-01T00:00:00Z --execute
```

The command deletes at most `--batch-size` rows per statement (default 5000,
maximum 5000) and reports the matched and deleted counts. `countBefore` is
evaluated first, so the dry run tells the operator the exact impact.

Before removing this feature entirely:

1. drop the trigger `trg_groups_rate_history`, then the function
   `record_group_rate_history()` and the table `group_rate_history` (this
   discards all history);
2. delete `backend/migrations/904_group_rate_history.sql` and the related Go
   code (`service/group_rate_signals.go`, `repository/group_rate_history_repo.go`,
   the DTO/handler wiring and frontend `GroupRateSignals` components);
3. delete this command and its README.

Existing migrations are immutable; do not edit `904_group_rate_history.sql`
after it has been applied.
