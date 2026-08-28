#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
profile="${1:-deterministic}"
evidence_dir="${RUNTIME_OPERATIONS_EVIDENCE_DIR:-${repo_root}/.artifacts/runtime-operations-reliability}"
run_started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
mkdir -p "${evidence_dir}"
cd "${repo_root}"

# Preflight external requirements before touching previously captured evidence.
case "${profile}" in
  real-dialects|load|ci|all)
    : "${RUNTIME_POSTGRES_TEST_DSN:?RUNTIME_POSTGRES_TEST_DSN is required}"
    ;;
esac
case "${profile}" in
  real-dialects|ci|all)
    : "${RUNTIME_MYSQL_TEST_DSN:?RUNTIME_MYSQL_TEST_DSN is required}"
    ;;
esac
case "${profile}" in
  drill)
    : "${RUNTIME_POSTGRES_TEST_DSN:?RUNTIME_POSTGRES_TEST_DSN is required}"
    : "${RUNTIME_MYSQL_TEST_DSN:?RUNTIME_MYSQL_TEST_DSN is required}"
    ;;
esac
case "${profile}" in
  drill|all)
    command -v docker >/dev/null
    command -v jq >/dev/null
    command -v sqlite3 >/dev/null
    ;;
esac

case "${profile}" in
  deterministic)
    rm -f "${evidence_dir}"/{correctness,worker-recovery,protocol-observability,operations-boundaries,migration-recovery,capacity}.log "${evidence_dir}/gate-summary-deterministic.json"
    ;;
  race)
    rm -f "${evidence_dir}"/{race-core,race-runtime-lifecycle}.log "${evidence_dir}/gate-summary-race.json"
    ;;
  real-dialects)
    rm -f "${evidence_dir}/real-dialects.log" "${evidence_dir}/gate-summary-real-dialects.json"
    ;;
  load)
    rm -f "${evidence_dir}"/{state-soak,queue-retry-backlog,postgres-pool-soak}.log "${evidence_dir}/gate-summary-load.json"
    ;;
  drill)
    rm -f "${evidence_dir}"/{migration-retirement,key-rotation-retention}.log "${evidence_dir}/gate-summary-drill.json" "${evidence_dir}"/disaster-recovery/runtime-dr-*.json
    ;;
  ci|all)
    rm -f "${evidence_dir}"/*.log "${evidence_dir}/gate-summary-${profile}.json" "${evidence_dir}"/disaster-recovery/runtime-dr-*.json
    ;;
esac

run_test() {
  local name="$1"
  shift
  printf '==> %s\n' "${name}"
  "$@" 2>&1 | tee "${evidence_dir}/${name}.log"
}

deterministic_gate() {
  run_test correctness go test -count=1 -timeout=5m \
    ./runtime/domain/operations/... \
    ./runtime/application/operations \
    ./runtime/infrastructure/persistence/database/operations
  run_test worker-recovery go test -count=1 -timeout=5m \
    ./runtime/platform/worker/... \
    ./runtime/application/integration \
    ./runtime/bootstrap/runtime
  run_test protocol-observability go test -count=1 -timeout=5m \
    ./runtime/transport/http \
    ./runtime/transport/http/openapi
  run_test operations-boundaries go test -count=1 -timeout=5m \
    -run 'TestEveryOperationsApplicationErrorHasMachineRunbook|TestRuntimeOpenAPIRemainsCodeFirst|TestRuntimeRoutesAndOpenAPIDoNotDrift|TestOperationsReliabilityReleaseEvidenceContractIsGoverned|TestHighRiskOwnerRoutesRegisterUnifiedTerminalReceipts' \
    ./runtime/boundary
  run_test migration-recovery go test -count=1 -timeout=5m \
    ./runtime/infrastructure/persistence/database/migration \
    ./scripts/operations/runtime_disaster_recovery
  run_test capacity go test -count=1 -timeout=5m \
    ./runtime/platform/capacity \
    ./runtime/platform/ratelimit \
    ./runtime/platform/resilience
}

race_gate() {
  run_test race-core go test -race -count=1 -timeout=20m \
    ./runtime/platform/worker/... \
    ./runtime/platform/capacity \
    ./runtime/platform/ratelimit \
    ./runtime/platform/resilience \
    ./runtime/application/lifecycle \
    ./runtime/infrastructure/persistence/database/lifecycle \
    ./runtime/infrastructure/persistence/database/operations
  run_test race-runtime-lifecycle go test -race -count=1 -timeout=5m \
    -run 'TestRuntimeStopWorkers|TestControlledWorkers|TestControlledInstance' \
    ./runtime/bootstrap/runtime
}

real_dialect_gate() {
  : "${RUNTIME_POSTGRES_TEST_DSN:?RUNTIME_POSTGRES_TEST_DSN is required}"
  : "${RUNTIME_MYSQL_TEST_DSN:?RUNTIME_MYSQL_TEST_DSN is required}"
  export RUNTIME_REQUIRE_REAL_DIALECTS=1
  # All packages intentionally target one shared external schema. Serialize
  # packages so destructive migration/retirement contracts cannot delete a
  # fixture owned by another package while tests inside each package still
  # exercise real database concurrency.
  run_test real-dialects go test -p=1 -count=1 -timeout=10m \
    ./runtime/infrastructure/persistence/database/agent \
    ./runtime/infrastructure/persistence/database/operations \
    ./runtime/infrastructure/persistence/database/dialecttest \
    ./runtime/infrastructure/persistence/database/automation \
    ./runtime/infrastructure/persistence/database/record
}

load_soak_gate() {
  : "${RUNTIME_POSTGRES_TEST_DSN:?RUNTIME_POSTGRES_TEST_DSN is required}"
  run_test state-soak go test -count=1 -timeout=5m \
    ./runtime/platform/capacity \
    -run '^TestBoundedRuntimeStateSoakDoesNotGrow$'
  run_test queue-retry-backlog go test -count=1 -timeout=5m \
    ./runtime/application/integration \
    ./runtime/infrastructure/persistence/database/record \
    -run '^(TestIntegrationWorkerPriorityQuotasKeepNewRetryAndReplayMoving|TestIntegrationQueuePressureActivatesAndRecoversAtThresholds|TestRecordBatchJobStoreFairClaimHeartbeatRetryAndQueueStats)$'
  run_test postgres-pool-soak env RUNTIME_REQUIRE_REAL_DIALECTS=1 go test -count=1 -timeout=10m \
    ./runtime/infrastructure/persistence/database/dialecttest \
    -run '^TestPostgresPoolSoakHasNoLeakIdleTransactionsOrRetryStorm$'
}

recovery_drill_gate() {
  RUNTIME_DRILL_SCHEMA_VERSION="$(sed -n 's/.*CurrentRuntimeSchemaVersion = "\([^"]*\)".*/\1/p' runtime/infrastructure/persistence/database/runtime_schema.go | head -n 1)" \
    scripts/operations/runtime_disaster_recovery/drill_external.sh "${evidence_dir}/disaster-recovery"
  run_test migration-retirement env RUNTIME_REQUIRE_REAL_DIALECTS=1 go test -count=1 -timeout=10m \
    ./runtime/infrastructure/persistence/database/migration \
    ./runtime/infrastructure/persistence/database/operations \
    -run '^(TestRuntimeSchemaMigrationBacksUpExistingDataAndRecordsVersion|TestRuntimeSchemaMigrationFailureLeavesDirtyLedger|TestDatabaseRetirementWriteProtectionAndQuarantineAcrossExternalDialects)$'
  run_test key-rotation-retention go test -count=1 -timeout=5m \
    ./runtime/infrastructure/plugins \
    ./runtime/application/lifecycle \
    ./runtime/infrastructure/persistence/database/lifecycle
}

case "${profile}" in
  deterministic)
    deterministic_gate
    ;;
  race)
    race_gate
    ;;
  real-dialects)
    real_dialect_gate
    ;;
  load)
    load_soak_gate
    ;;
  drill)
    recovery_drill_gate
    ;;
  ci)
    deterministic_gate
    race_gate
    real_dialect_gate
    ;;
  all)
    deterministic_gate
    race_gate
    real_dialect_gate
    recovery_drill_gate
    ;;
  *)
    printf 'unknown profile %q; use deterministic, race, real-dialects, load, drill, ci, or all\n' "${profile}" >&2
    exit 2
    ;;
esac

run_completed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
export RUNTIME_OPERATIONS_GATE_PROFILE="${profile}"
export RUNTIME_OPERATIONS_GATE_STARTED="${run_started}"
export RUNTIME_OPERATIONS_GATE_COMPLETED="${run_completed}"
export RUNTIME_OPERATIONS_GATE_EVIDENCE="${evidence_dir}"
python3 - <<'PY'
import hashlib
import json
import os
import platform
import subprocess
from datetime import date
from pathlib import Path

root = Path.cwd()
evidence = Path(os.environ["RUNTIME_OPERATIONS_GATE_EVIDENCE"])

def output(*command):
    return subprocess.check_output(command, cwd=root, text=True).strip()

def digest(path):
    return hashlib.sha256((root / path).read_bytes()).hexdigest()

def digest_tree(path):
    sha = hashlib.sha256()
    for item in sorted((root / path).rglob("*.go")):
        sha.update(item.relative_to(root).as_posix().encode())
        sha.update(item.read_bytes())
    return sha.hexdigest()

flaky_path = root / "scripts/operations/contracts/runtime-reliability-flaky-tests.json"
flaky = json.loads(flaky_path.read_text())
if flaky.get("version") != "runtime.reliability.flaky.v1" or not isinstance(flaky.get("entries"), list):
    raise SystemExit("invalid flaky-test registry contract")
for entry in flaky["entries"]:
    missing = [field for field in ("test", "owner", "reason", "expires_on") if not str(entry.get(field, "")).strip()]
    if missing:
        raise SystemExit(f"flaky-test registry entry is missing {missing}")
    if date.fromisoformat(entry["expires_on"]) < date.today():
        raise SystemExit(f"flaky-test registry entry expired: {entry['test']}")

drills = []
for path in sorted((evidence / "disaster-recovery").glob("runtime-dr-*.json")):
    item = json.loads(path.read_text())
    drills.append({
        "engine": item.get("engine"),
        "actual_rpo_seconds": item.get("actual_rpo_seconds"),
        "actual_rto_seconds": item.get("actual_rto_seconds"),
        "evidence": str(path.relative_to(evidence)),
    })

summary = {
    "evidence_version": "runtime.operations.reliability.v1",
    "profile": os.environ["RUNTIME_OPERATIONS_GATE_PROFILE"],
    "status": "passed",
    "started_at": os.environ["RUNTIME_OPERATIONS_GATE_STARTED"],
    "completed_at": os.environ["RUNTIME_OPERATIONS_GATE_COMPLETED"],
    "commit": output("git", "rev-parse", "HEAD"),
    "runtime_version": output("git", "describe", "--always", "--dirty"),
    "dirty_worktree": bool(output("git", "status", "--porcelain")),
    "runtime_schema_version": next(
        line.split('"')[1]
        for line in (root / "runtime/infrastructure/persistence/database/runtime_schema.go").read_text().splitlines()
        if "CurrentRuntimeSchemaVersion" in line
    ),
    "contracts": {
        "openapi_contract_sha256": digest_tree("runtime/transport/http/openapi"),
        "operations_inventory_sha256": digest("docs/architecture/runtime-operations-inventory.md"),
        "runbook_sha256": digest("scripts/operations/contracts/runtime-operations-reliability-runbook.md"),
        "config_contract_sha256": digest_tree("runtime/platform/config"),
    },
    "environment": {
        "go": output("go", "version"),
        "os": platform.platform(),
        "postgres_real": bool(os.environ.get("RUNTIME_POSTGRES_TEST_DSN")),
        "mysql_real": bool(os.environ.get("RUNTIME_MYSQL_TEST_DSN")),
    },
    "external_dependency_levels": {
        "deterministic_mock": "scripted clock, identifiers, randomness, faults, and provider outcomes",
        "sandbox": "disposable SQLite and Docker database engines",
        "real_provider": "not required by the Operations release gate",
    },
    "test_logs": sorted(path.name for path in evidence.glob("*.log")),
    "flaky_registry": str(flaky_path.relative_to(root)),
    "flaky_tests": flaky["entries"],
    "baseline_policy": "scripts/operations/contracts/runtime-operations-reliability-baseline.md",
    "recovery_drills": drills,
}
serialized = json.dumps(summary, indent=2, sort_keys=True) + "\n"
(evidence / f"gate-summary-{summary['profile']}.json").write_text(serialized)
(evidence / "gate-summary.json").write_text(serialized)
PY

printf 'Runtime operations reliability gate passed (%s). Evidence: %s\n' "${profile}" "${evidence_dir}/gate-summary.json"
