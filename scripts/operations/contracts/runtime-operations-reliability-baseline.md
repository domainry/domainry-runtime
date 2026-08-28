# Runtime Operations Reliability Baseline

The release gate has no approved ignored failure. A failure is classified as:

- `regression`: a command selected by the Operations reliability gate fails;
- `repository_baseline`: a test outside the selected gate already fails on the
  same commit and is recorded with its command and output separately;
- `environment`: a required tool, DSN, credential or service is unavailable.

All three classifications fail the relevant production acceptance step. The
classification exists to preserve causality, not to turn a failure green.
Entries must include the failing command, commit, observed error, owner and a
review deadline. Removing or skipping the test is not remediation.

Current approved baseline entries: none.

## Local dirty-worktree observations (not waivers)

| Observed | Command | Failure | Suggested owner | Review by |
| --- | --- | --- | --- | --- |
| 2026-07-19 | `go test ./runtime/boundary` | concurrent repository changes left architecture counts, inventories, context exceptions and transaction/worker markers out of sync; Operations-selected boundary tests pass | Runtime architecture owners | 2026-07-26 |
| 2026-07-19 | `go test ./runtime/bootstrap/integrationtest -run TestSchedulerAPISmokeVerifiesRuntimeOperationsAndEvidence` | startup stops in lifecycle policy installation: `status retention must name a status group and meet the policy minimum`, before Scheduler routing executes | Lifecycle owner | 2026-07-26 |

These observations came from an already dirty shared worktree and are not part
of the Operations gate's passing result. They must be rechecked on the merged
commit; the release workflow remains fail-closed.
