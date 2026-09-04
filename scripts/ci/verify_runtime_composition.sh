#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
profile="${1:-pr}"
export GOWORK=off
cd "${repo_root}"

case "${profile}" in
  pr|heavy) ;;
  *)
    printf 'unknown Runtime composition profile %q; use pr or heavy\n' "${profile}" >&2
    exit 2
    ;;
esac

printf '==> verify pinned module graph\n'
go mod verify
go mod tidy -diff

printf '==> run exact-version 11-Binding composition smoke\n'
go test -count=1 -timeout=10m ./runtime/bootstrap/runtime \
  -run '^(TestPinnedModuleSetLockMatchesGoMod|TestPinnedModuleSetComposesAllElevenBindingsOnOneHostDatabase|TestIntegrationSaaSTopologyDoesNotStartOwnerWorkers|TestRuntimeAuthorizationRegistryUsesModuleActionAsSingleRouteAuthority|TestRuntimeIntegrationTriggerSinkExecutesOnlyRuntimeOwnedTargets|TestRuntimePermissionReconcileCompensatesEarlierOwnersOnBatchFailure|TestNewPropagatesNotificationAndServiceAssemblyFailures)$'
go test -count=1 -timeout=10m ./pkg/runtimehost \
  -run '^TestProjectMainCompilesUsing'

printf '==> run cross-module golden chains\n'
go test -count=1 -timeout=10m ./runtime/bootstrap/integrationtest \
  -run '^(TestProjectActionNotificationDispatchRealRuntimeReplayConcurrencyLifecycleAndRestart|TestBusinessActorRecordActionWorkflowTaskAndRefreshEndToEnd|TestRuntimeBusinessEventStreamConnectsReplaysAndRejectsCrossTenant)$'
go test -count=1 -timeout=5m ./runtime/application/report/export/application \
  -run '^(TestNewReportExportJobWritesOnlyDataExchange|TestReportDataExchangeProviderPagesAndFinalizesBusinessProjection)$'

printf '==> run shared transaction, rollback, outbox, idempotency, and restart smoke\n'
go test -count=1 -timeout=10m \
  ./runtime/infrastructure/persistence/database/action \
  ./runtime/infrastructure/persistence/database/notificationpublication \
  ./runtime/infrastructure/persistence/database/transaction \
  ./runtime/infrastructure/persistence/database/workflow

if [[ "${profile}" == "pr" ]]; then
  exit 0
fi

: "${RUNTIME_POSTGRES_TEST_DSN:?RUNTIME_POSTGRES_TEST_DSN is required by the heavy composition gate}"
: "${RUNTIME_MYSQL_TEST_DSN:?RUNTIME_MYSQL_TEST_DSN is required by the heavy composition gate}"
export RUNTIME_REQUIRE_REAL_DIALECTS=1

printf '==> run composition and golden-chain race gate\n'
go test -race -count=1 -timeout=20m ./runtime/bootstrap/runtime ./runtime/bootstrap/integrationtest \
  -run '^(TestPinnedModuleSetComposesAllElevenBindingsOnOneHostDatabase|TestProjectActionNotificationDispatchRealRuntimeReplayConcurrencyLifecycleAndRestart|TestBusinessActorRecordActionWorkflowTaskAndRefreshEndToEnd|TestRuntimeBusinessEventStreamConnectsReplaysAndRejectsCrossTenant)$'
go test -race -count=1 -timeout=20m \
  ./runtime/infrastructure/persistence/database/action \
  ./runtime/infrastructure/persistence/database/notificationpublication \
  ./runtime/infrastructure/persistence/database/transaction \
  ./runtime/infrastructure/persistence/database/workflow \
  -run '^(TestBusinessActionExecutionStoreHundredConcurrentClaimsHaveOneOwner|TestRelayFencesConcurrentDuplicateWorkers|TestSQLUnitOfWorkRollsBackCancellationBeforeCommit|TestWorkflowExecutionReceiptStoreHundredConcurrentClaimsHaveOneOwner)$'

printf '==> run all-module real-database restart gate\n'
go test -p=1 -count=1 -timeout=20m ./runtime/bootstrap/runtime \
  -run '^TestPinnedModuleSetComposesAllElevenBindingsAcrossRealDialects$'

printf '==> run persistence recovery and real-dialect gates\n'
go test -p=1 -count=1 -timeout=20m \
  ./runtime/infrastructure/persistence/database/action \
  ./runtime/infrastructure/persistence/database/notificationpublication \
  ./runtime/infrastructure/persistence/database/operations \
  ./runtime/infrastructure/persistence/database/dialecttest \
  ./runtime/infrastructure/persistence/database/failure \
  ./runtime/infrastructure/persistence/database/transaction \
  ./runtime/infrastructure/persistence/database/workflow
