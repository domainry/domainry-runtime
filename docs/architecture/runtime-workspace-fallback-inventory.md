# Runtime Workspace Fallback Inventory

This is the conservative production-code scan for empty Runtime workspace
fallbacks and hard-coded `default` values in Application and Infrastructure.
`TestRuntimeWorkspaceFallbackReviewBaseline` enforces the exact, content-bound
finding list in `runtime-workspace-fallback-review-baseline.txt`. A new or
changed candidate fails the Runtime boundary suite; removing a fallback must be
followed by tightening that baseline.

Scan scope: production `*.go` files below `runtime/application` and
`runtime/infrastructure`; `*_test.go` is excluded. The scan matches
empty workspace comparisons, workspace/default proximity, and literal
`"default"` / `'default'` values.

The scanner records production lines containing `workspace` together with an
empty-string comparison or the literal `"default"`. It intentionally finds
both real fallbacks and explicit system-scope branches: the baseline records
that each occurrence was adjudicated, not that fallback behavior is generally
authorized.

The acceptance-fixture setup entry is an explicit fail-closed boundary, not a
fallback: an empty committed workspace rejects startup fixture materialization.
The caller additionally gates this path to an explicitly enabled `acceptance`
environment backed by SQLite, so no default workspace or production path is
introduced by accepting this exact finding into the review baseline.

The controlled cross-Workspace aggregate entries are also fail-closed checks,
not fallbacks. The Action execution entry rejects a missing mandatory audit
port. The persistence entries reject an invalid catalog row and reject an
empty explicit Workspace set; neither condition selects a default Workspace
or turns an empty set into installation-wide access.

The Workspace Identity usage entries are fail-closed validation and binding
checks. They reject blank, duplicated, inactive or mismatched physical
Workspace identities before projecting canonical Workspace codes. The cursor
entries require every authorization binding, including Workspace and
authorization revision, and never substitute a default. The additional
aggregate-store entry rejects a blank canonical Workspace code returned by the
active installation catalog.
