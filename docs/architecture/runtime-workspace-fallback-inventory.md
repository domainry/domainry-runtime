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
