package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransactionContractIsStorageNeutralAndOwnerScoped(t *testing.T) {
	root := runtimeRoot(t)
	path := filepath.Join(root, "domain", "transaction", "contract", "transaction_unit_of_work.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{"type PortSet interface", "type Operation[Ports PortSet]", "type UnitOfWork[Ports PortSet] interface", "WithinTransaction(context.Context, Operation[Ports]) error"} {
		if !strings.Contains(content, required) {
			t.Errorf("transaction contract missing %q", required)
		}
	}
	for _, forbidden := range []string{"database/sql", "*sql.Tx", "runtime/infrastructure", "runtime/transport"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("transaction contract exposes forbidden dependency %q", forbidden)
		}
	}
}

func TestSchedulerTransactionPortsStayUseCaseScoped(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "domain", "scheduler", "contract", "scheduler_transaction_ports.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{"type SchedulerRunCommandTransactionPorts interface", "UpdateRunIfCurrent", "AppendRunEvent", "InsertAudit"} {
		if !strings.Contains(content, required) {
			t.Errorf("scheduler transaction port set missing %q", required)
		}
	}
	for _, forbidden := range []string{"RuntimeStore", "RecordRepository", "Workflow", "Connector", "File", "database/sql", "infrastructure/"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("scheduler transaction port set exposes broad/forbidden capability %q", forbidden)
		}
	}
}

func TestTransactionIsolationPolicyIsStorageNeutralAndExplicit(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "domain", "transaction", "contract", "transaction_options.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{"IsolationReadCommitted", "IsolationRepeatableRead", "IsolationSerializable", "type Options struct"} {
		if !strings.Contains(content, required) {
			t.Errorf("transaction isolation contract missing %q", required)
		}
	}
	if strings.Contains(content, "database/sql") || strings.Contains(content, "sql.TxOptions") {
		t.Fatal("transaction isolation contract exposes SQL implementation")
	}
}

func TestUnitOfWorkRejectsSilentNestedTransactions(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database", "transaction", "unit_of_work.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{"ErrNestedUnitOfWork", "explicit savepoint contract", "transactioncontract.ActiveTransaction", "transactioncontract.WithActiveTransaction"} {
		if !strings.Contains(content, required) {
			t.Errorf("unit of work nesting gate missing %q", required)
		}
	}
}

func TestUnitOfWorkRetryRequiresTransactionalOnlySideEffects(t *testing.T) {
	root := runtimeRoot(t)
	contract, err := os.ReadFile(filepath.Join(root, "domain", "transaction", "contract", "transaction_options.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"TransactionRetryPolicy", "TransactionSideEffectsTransactionalOnly", "TransactionSideEffectsMayEscape", "TransactionIdempotencyScope", "IdempotencyScope", "MaxTransactionRetryAttempts", "MaxAttempts", "InitialBackoff", "MaxBackoff", "Deadline", "RetryBudget"} {
		if !strings.Contains(string(contract), required) {
			t.Errorf("transaction retry safety contract missing %q", required)
		}
	}
	implementation, err := os.ReadFile(filepath.Join(root, "infrastructure", "persistence", "database", "transaction", "unit_of_work.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ErrUnitOfWorkRetryUnsafe", "ErrUnitOfWorkRetryIdentity", "withinTransactionAttempt", "IsTransactionTransient", "WithTransactionRetryIdentity", "TransactionObservation", "Rollbacks", "Retries", "Conflicts", "Duration"} {
		if !strings.Contains(string(implementation), required) {
			t.Errorf("unit of work retry gate missing %q", required)
		}
	}
}

func TestAfterCommitContractAllowsOnlyRecoverableLocalCoordination(t *testing.T) {
	root := runtimeRoot(t)
	contract, err := os.ReadFile(filepath.Join(root, "domain", "transaction", "contract", "transaction_after_commit.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"AfterCommitDurableWorkWakeup", "AfterCommitCacheInvalidation", "AfterCommitLocalProjectionRefresh", "DurableRecovery", "ErrAfterCommitRecoveryRequired"} {
		if !strings.Contains(string(contract), required) {
			t.Errorf("after-commit code contract missing %q", required)
		}
	}
	document, err := os.ReadFile(filepath.Join(root, "..", "docs", "architecture", "runtime-after-commit-hook-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"email or Webhook delivery", "external Provider/API calls", "authoritative business mutations", "file publication", "commit remains successful", "must not reinterpret it as a rollback"} {
		if !strings.Contains(string(document), required) {
			t.Errorf("after-commit failure contract missing %q", required)
		}
	}
}

func TestAsyncEffectsHaveDurableFactsBeforePostCommitDispatch(t *testing.T) {
	root := runtimeRoot(t)
	for _, relative := range []string{
		"application/record/record_create_application_service.go",
		"application/record/record_update_application_service.go",
		"application/record/record_delete_application_service.go",
		"application/record/record_restore_application_service.go",
	} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		intent := strings.Index(content, "commit.WorkflowIntents")
		commit := strings.Index(content, "MutationKernel.Commit")
		dispatch := strings.Index(content, "ExecuteWorkflow(ctx, commit.WorkflowIntents")
		planning := intent
		if delegated := strings.Index(content, "planned, err := s.planCreate"); delegated >= 0 {
			planning = delegated
			dispatch = strings.Index(content, "ExecuteWorkflow(ctx, planned.commit.WorkflowIntents")
		}
		if delegated := strings.Index(content, "planned, err := s.planUpdate"); delegated >= 0 {
			planning = delegated
			dispatch = strings.Index(content, "ExecuteWorkflow(ctx, planned.commit.WorkflowIntents")
		}
		if delegated := strings.Index(content, "planned, err := s.planRestore"); delegated >= 0 {
			planning = delegated
			dispatch = strings.Index(content, "ExecuteWorkflow(ctx, planned.commit.WorkflowIntents")
		}
		if delegated := strings.Index(content, "plan, record, err := s.PlanRestoreMutation"); delegated >= 0 {
			planning = delegated
			dispatch = strings.Index(content, "ExecuteWorkflow(ctx, plan.CanonicalCommit().WorkflowIntents")
		}
		if delegated := strings.Index(content, "if err := s.planDelete"); delegated >= 0 {
			planning = delegated
			dispatch = strings.Index(content, "ExecuteWorkflow(ctx, effect.commit.WorkflowIntents")
		}
		if delegated := strings.Index(content, "group, err := s.planDeleteMutation"); delegated >= 0 {
			planning = delegated
			dispatch = strings.Index(content, "ExecuteWorkflow(ctx, effect.commit.WorkflowIntents")
		}
		if intent < 0 || planning < 0 || commit < 0 || dispatch < 0 || planning >= commit || commit >= dispatch {
			t.Errorf("%s must persist Workflow intent before commit and dispatch only after commit", relative)
		}
	}
	outbox, err := os.ReadFile(filepath.Join(root, "application", "integration", "integration_application_delivery_commands.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"IntegrationOutboxMessage", "Status: \"queued\"", "deliveryRepo.InsertOutbox(ctx", "RequestRef:"} {
		if !strings.Contains(string(outbox), required) {
			t.Errorf("Integration async delivery path missing durable Outbox evidence %q", required)
		}
	}
	workflow, err := os.ReadFile(filepath.Join(root, "application", "workflow", "workflow_record_execution_application_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	claim := strings.Index(string(workflow), "UpdateExecutionWhere(ctx, principal.WorkspaceID, claimed")
	execute := strings.Index(string(workflow), "executeWorkflowAttempt(ctx, workflow")
	if claim < 0 || execute < 0 || claim >= execute {
		t.Fatal("committed Workflow intent must be claimed before fast-path execution")
	}
}

func TestConnectorFilesystemPublicationUsesRootConfinementAndDurableStaging(t *testing.T) {
	repository := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	filesystem, err := os.ReadFile(filepath.Join(repository, "pkg", "runtimehost", "connector_filesystem_transport.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"os.OpenRoot", "root.OpenFile", "os.O_EXCL", "stage.Sync", "root.Rename", "ctx.Err"} {
		if !strings.Contains(string(filesystem), required) {
			t.Errorf("Runtime filesystem transport missing confinement/publication marker %q", required)
		}
	}
}

func TestWorkerLifecycleRetainsPollingRecoveryWhenWakeupIsLost(t *testing.T) {
	root := runtimeRoot(t)
	lifecycle, err := os.ReadFile(filepath.Join(root, "bootstrap", "runtime", "worker_lifecycle.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"StartWorkflowWorker", "StartIntegrationEventWorker", "StartIntegrationOutboxWorker", "startIntegrationInvocationReconciliationWorker", "StartNotificationPublicationWorker"} {
		if !strings.Contains(string(lifecycle), required) {
			t.Errorf("Runtime worker lifecycle missing durable polling loop %q", required)
		}
	}
	loop, err := os.ReadFile(filepath.Join(root, "platform", "worker", "lifecycle.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"runTick(ctx, name, tick)", "time.NewTicker(interval)", "case <-ticker.C"} {
		if !strings.Contains(string(loop), required) {
			t.Errorf("worker polling recovery contract missing %q", required)
		}
	}
}

func TestExternalReceiptReconciliationRecognizesDurableBusinessFact(t *testing.T) {
	root := runtimeRoot(t)
	repository, err := os.ReadFile(filepath.Join(root, "domain", "integration", "repository", "integration_repository.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"IntegrationInvocationReconciliationRepository", "ListPreparedInvocationsForReconciliation", "MarkInvocationReconciliationRequired", "compare-and-swap"} {
		if !strings.Contains(string(repository), required) {
			t.Errorf("external receipt reconciliation repository contract missing %q", required)
		}
	}
	application, err := os.ReadFile(filepath.Join(root, "application", "integration", "integration_invocation_reconciliation_application_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ReconcileMissingIntegrationReceipts", "reconciliation_required", "business_fact_exists", "external_receipt_missing", "StartNamedLoop"} {
		if !strings.Contains(string(application), required) {
			t.Errorf("external receipt reconciliation task missing %q", required)
		}
	}
	persistence, err := os.ReadFile(filepath.Join(root, "infrastructure", "persistence", "database", "integration", "integration_delivery_store.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`ormbuilder.Equal("status", "prepared")`, `ormbuilder.Equal("response_ref", "")`, "backend.integration.invocation.external_receipt_missing"} {
		if !strings.Contains(string(persistence), required) {
			t.Errorf("external receipt reconciliation persistence missing %q", required)
		}
	}
}

func TestCommitFailureProofChecksNoExecutableDurableWork(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database", "transaction", "unit_of_work_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"InsertDurableWork", "InsertDeferredViolation", "commit failure left executable durable work", "status = 'queued'"} {
		if !strings.Contains(string(raw), required) {
			t.Errorf("commit failure durable-work proof missing %q", required)
		}
	}
}

func TestEveryCriticalTransactionChainHasFourFailureWindows(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database", "transaction", "unit_of_work_hooks_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{
		"TestCriticalTransactionChainsCoverEveryFailureWindow",
		"record_mutation", "workflow_task_decision", "action_execution", "scheduler_run_state",
		"integration_event_acceptance", "metadata_publication", "cross_boundary_durable_intent",
		"first_write", "middle_write", "before_commit", "after_commit",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("critical-chain failure-window matrix missing %q", required)
		}
	}
}

func TestHundredConcurrentRecordUpdatesProveNoLostUpdateOrPartialCommit(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database", "record", "record_mutation_execution_store_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"TestRecordMutationHundredConcurrentUpdatesHaveNoLostUpdateOrPartialCommit",
		"index < 100", "ExpectedUpdatedAt: initial.UpdatedAt", "succeeded.Load() != 1",
		"conflicted.Load()+transientlyRejected.Load() != 99", "winnerCount != 1",
		"_audit_events", "integration_outbox_messages", "_workflow_executions",
	} {
		if !strings.Contains(string(raw), required) {
			t.Errorf("100-concurrent-update proof missing %q", required)
		}
	}
}

func TestUnitOfWorkCancelTimeoutAndPanicAllProveRollback(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database", "transaction", "unit_of_work_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"TestSQLUnitOfWorkRollsBackCancellationBeforeCommit",
		"TestSQLUnitOfWorkRollsBackDeadlineExceededAfterWriteBeforeCommit",
		"TestSQLUnitOfWorkRollsBackAndRepanics",
		"context.DeadlineExceeded", "deadline-exceeded transaction committed", "transaction panic",
	} {
		if !strings.Contains(string(raw), required) {
			t.Errorf("cancel/timeout/panic rollback proof missing %q", required)
		}
	}
}

func TestPostgresAndMySQLUseRealDeadlockAndSerializableConflictProofs(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database", "dialecttest", "real_lock_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"RUNTIME_POSTGRES_TEST_DSN", "RUNTIME_MYSQL_TEST_DSN",
		"TestRealDialectDeadlockContracts", "TestRealDialectSerializableConflictContracts",
		"UPDATE runtime_lock_contract SET value_text = 'a-two' WHERE id = 'two'",
		"UPDATE runtime_lock_contract SET value_text = 'b-one' WHERE id = 'one'",
		"TransactionTransientDeadlock", "TransactionTransientSerializationFailure",
	} {
		if !strings.Contains(string(raw), required) {
			t.Errorf("real dialect conflict proof missing %q", required)
		}
	}
}

func TestSchedulerCallerCommandsCommitStateAndEventAtomically(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "application", "scheduler", "scheduler_operations.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	functions := []string{"SimulateJob", "RetryRun", "CancelRun", "ResolveDeadLetter"}
	for index, name := range functions {
		start := strings.Index(content, "func (s *SchedulerApplicationService) "+name+"(")
		if start < 0 {
			t.Fatalf("scheduler command %s missing", name)
		}
		end := len(content)
		if index+1 < len(functions) {
			if next := strings.Index(content[start+1:], "func (s *SchedulerApplicationService) "+functions[index+1]+"("); next >= 0 {
				end = start + 1 + next
			}
		}
		body := content[start:end]
		for _, required := range []string{"schedulerRunEventRecord", "CommitRecordMutationBatch"} {
			if !strings.Contains(body, required) {
				t.Errorf("scheduler command %s lacks atomic state/event boundary %q", name, required)
			}
		}
	}
}

func TestWorkflowRecoveryUsesAtomicStateCommitAndDecisionFailsClosed(t *testing.T) {
	root := runtimeRoot(t)
	store, err := os.ReadFile(filepath.Join(root, "infrastructure", "persistence", "database", "workflow", "workflow_decision_store.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"CommitWorkflowState", "updateNodeTx", "updateTaskTx", "updateProcessTx", "insertEventTx", "tx.Commit"} {
		if !strings.Contains(string(store), required) {
			t.Errorf("workflow atomic state store missing %q", required)
		}
	}
	recovery, err := os.ReadFile(filepath.Join(root, "application", "workflow", "workflow_process_decision_recovery_application_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recovery), "CommitWorkflowState") {
		t.Fatal("workflow decision recovery bypasses atomic state commit")
	}
	application, err := os.ReadFile(filepath.Join(root, "application", "workflow", "workflow_process_application_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ResolveWorkflowProcessFailure", "CommitWorkflowState", "transactional workflow decision store unavailable"} {
		if !strings.Contains(string(application), required) {
			t.Errorf("workflow failure/decision gate missing %q", required)
		}
	}
}

func TestUpperLayersNeverOwnDatabaseTransactions(t *testing.T) {
	root := runtimeRoot(t)
	for _, layer := range []string{"application", "transport", "bootstrap"} {
		err := filepath.Walk(filepath.Join(root, layer), func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, forbidden := range []string{"BeginTx(", "*sql.Tx", "sql.TxOptions", ".Commit()", ".Rollback()"} {
				if strings.Contains(string(raw), forbidden) {
					t.Errorf("%s layer owns forbidden transaction primitive %q in %s", layer, forbidden, path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCrashAfterCommitRecoveryReopensStoreBeforeWorkerClaim(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database", "integration", "worker_store_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"TestOutboxCrashAfterCommitIsRecoveredByReopenedWorkerProcess",
		"producerStore.Close()", "recoveredStore := open()", "ListDueOutbox", "ClaimOutbox",
		"process disappearing immediately after the database commit",
	} {
		if !strings.Contains(string(raw), required) {
			t.Errorf("crash-after-commit recovery proof missing %q", required)
		}
	}
}

func TestCrossBoundaryConsistencyContractHasDurableIntentBeforeSideEffect(t *testing.T) {
	root := runtimeRoot(t)
	document, err := os.ReadFile(filepath.Join(root, "..", "docs", "architecture", "runtime-cross-boundary-consistency-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"transaction_boundary_intents", "reconciliation_required", "compensating", "compensated", "manual_review", "fencing token", "integration_invocations(status=prepared)"} {
		if !strings.Contains(string(document), required) {
			t.Errorf("cross-boundary contract missing %q", required)
		}
	}
	source, err := os.ReadFile(filepath.Join(root, "application", "integration", "integration_application_sync_call.go"))
	if err != nil {
		t.Fatal(err)
	}
	prepared := strings.Index(string(source), "InsertInvocation(ctx, invocation.WorkspaceID, invocation)")
	external := strings.Index(string(source), "prepared.Execute(")
	if prepared < 0 || external < 0 || prepared >= external {
		t.Fatal("sync Provider call must follow durable prepared invocation persistence")
	}
}

func TestOptimisticConcurrencyUsesCanonicalPreconditionContract(t *testing.T) {
	root := runtimeRoot(t)
	contract, err := os.ReadFile(filepath.Join(root, "domain", "transaction", "model", "transaction_commit.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"type OptimisticPrecondition struct", "ExpectedVersion", "ExpectedUpdatedAt", "OptimisticUpdatedAt()"} {
		if !strings.Contains(string(contract), required) {
			t.Errorf("optimistic concurrency contract missing %q", required)
		}
	}
	applicationRoot := filepath.Join(root, "application")
	err = filepath.Walk(applicationRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), "ExpectedUpdatedAt:") &&
			!strings.Contains(string(raw), "OptimisticPrecondition{ExpectedUpdatedAt:") &&
			!strings.Contains(string(raw), "ExpectedUpdatedAt: commit.OptimisticUpdatedAt()") {
			t.Errorf("Application bypasses canonical optimistic precondition: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
