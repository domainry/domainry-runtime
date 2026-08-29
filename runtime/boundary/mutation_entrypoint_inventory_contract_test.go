package boundary_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mutationEntrypointInventory struct {
	Version     string                             `json:"version"`
	TargetModel string                             `json:"target_model"`
	Entries     []mutationEntrypointInventoryEntry `json:"entries"`
}

type mutationEntrypointInventoryEntry struct {
	Key                   string          `json:"key"`
	Category              string          `json:"category"`
	Operations            []string        `json:"operations"`
	SourceFiles           []string        `json:"source_files"`
	Authorizer            string          `json:"authorizer"`
	SchemaDefaultsOwner   string          `json:"schema_defaults_owner"`
	ValidationOwner       string          `json:"validation_owner"`
	IdempotencyOwner      string          `json:"idempotency_owner"`
	TransactionOwner      string          `json:"transaction_owner"`
	AuditOwner            string          `json:"audit_owner"`
	OutboxWorkflowOwner   string          `json:"outbox_workflow_owner"`
	PostCommitOwner       string          `json:"post_commit_owner"`
	FailureSemantics      string          `json:"failure_semantics"`
	CurrentExecutionOwner string          `json:"current_execution_owner"`
	KernelStatus          string          `json:"kernel_status"`
	SemanticFlags         map[string]bool `json:"semantic_flags"`
}

func TestMutationEntrypointInventoryOwnsEveryDeclaredWritePath(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "mutation_entrypoint_inventory_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory mutationEntrypointInventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Version != "runtime.mutation-entrypoints.v1" || strings.TrimSpace(inventory.TargetModel) == "" {
		t.Fatalf("inventory header=%#v", inventory)
	}
	requiredCategories := map[string]bool{
		"http_direct_crud": false, "agent_tools": false, "business_action": false,
		"workflow": false, "automation_before": false, "automation_after": false, "state_machine": false,
		"pipeline": false, "import_batch": false, "scheduler": false, "integration": false,
		"internal_lifecycle": false, "business_seed": false,
	}
	requiredFlags := []string{"before_automation", "after_automation", "scheduler_validation", "identity_projection", "workflow_intent", "transactional_outbox"}
	seenKeys := map[string]bool{}
	repositoryRoot := filepath.Join("..", "..")
	for index, entry := range inventory.Entries {
		if strings.TrimSpace(entry.Key) == "" || seenKeys[entry.Key] {
			t.Fatalf("entry[%d] key missing or duplicated: %q", index, entry.Key)
		}
		seenKeys[entry.Key] = true
		if _, exists := requiredCategories[entry.Category]; !exists {
			t.Fatalf("entry %s has unknown category %q", entry.Key, entry.Category)
		}
		requiredCategories[entry.Category] = true
		for field, value := range map[string]string{
			"authorizer": entry.Authorizer, "schema_defaults_owner": entry.SchemaDefaultsOwner, "validation_owner": entry.ValidationOwner,
			"idempotency_owner": entry.IdempotencyOwner, "transaction_owner": entry.TransactionOwner, "audit_owner": entry.AuditOwner,
			"outbox_workflow_owner": entry.OutboxWorkflowOwner, "post_commit_owner": entry.PostCommitOwner,
			"failure_semantics": entry.FailureSemantics, "current_execution_owner": entry.CurrentExecutionOwner, "kernel_status": entry.KernelStatus,
		} {
			if strings.TrimSpace(value) == "" {
				t.Fatalf("entry %s missing %s", entry.Key, field)
			}
		}
		if len(entry.Operations) == 0 || len(entry.SourceFiles) == 0 {
			t.Fatalf("entry %s has no operation or source evidence", entry.Key)
		}
		for _, source := range entry.SourceFiles {
			if !strings.HasPrefix(source, "runtime/") {
				t.Fatalf("entry %s source outside Runtime: %q", entry.Key, source)
			}
			if info, err := os.Stat(filepath.Join(repositoryRoot, source)); err != nil || info.IsDir() {
				t.Fatalf("entry %s source does not exist: %q err=%v", entry.Key, source, err)
			}
		}
		for _, flag := range requiredFlags {
			if _, exists := entry.SemanticFlags[flag]; !exists {
				t.Fatalf("entry %s missing semantic flag %s", entry.Key, flag)
			}
		}
	}
	for category, present := range requiredCategories {
		if !present {
			t.Errorf("mutation entrypoint category is not inventoried: %s", category)
		}
	}
}

func TestNonInteractiveMutationEntrypointsHaveExplicitCanonicalOrInternalDecision(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "mutation_entrypoint_inventory_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory mutationEntrypointInventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"record_import_batch":                 "canonical_facade",
		"record_timer_internal_records":       "audited_internal_owner",
		"integration_callback_mutation":       "canonical_facade",
		"subject_lifecycle_internal_mutation": "audited_internal_owner",
		"business_seed_records":               "installation_internal_owner",
	}
	for _, entry := range inventory.Entries {
		if status, ok := want[entry.Key]; ok {
			if entry.KernelStatus != status {
				t.Errorf("entry %s status=%q want=%q", entry.Key, entry.KernelStatus, status)
			}
			delete(want, entry.Key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing mutation decisions: %#v", want)
	}

	repositoryRoot := filepath.Join("..", "..")
	read := func(relative string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	for relative, required := range map[string]string{
		"runtime/application/record/record_import_application_service.go":                             "dependencies.CreateRecord(ctx",
		"runtime/application/integration/integration_entrypoint_orchestration_application_service.go": "recordsApp.UpdateRecord(ctx",
	} {
		content := read(relative)
		if !strings.Contains(content, required) {
			t.Errorf("%s missing canonical facade evidence %q", relative, required)
		}
		for _, forbidden := range []string{".InsertRecord(", ".CommitRecordMutation(", ".CommitRecordMutationBatch("} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s bypasses canonical Record facade with %q", relative, forbidden)
			}
		}
	}
	schedulerWiring := read("runtime/bootstrap/composition/runtime_services_record_policy.go")
	for _, required := range []string{"RecordInternalMutationSchedulerRuntime", "internalMutations.Insert", "internalMutations.Update"} {
		if !strings.Contains(schedulerWiring, required) {
			t.Errorf("Scheduler internal mutation allowlist missing %q", required)
		}
	}
	internalPolicy := read("runtime/application/record/record_internal_mutation_application_service.go")
	for _, required := range []string{"RecordValidateInternalMutationPolicy", "record_timer", "record_timer_event", "report_query_run", "backend.record.internal_mutation_policy_denied"} {
		if !strings.Contains(internalPolicy, required) {
			t.Errorf("internal mutation policy missing %q", required)
		}
	}
}

func TestMutationKernelADRFreezesCommandCRUDAndAutomationBoundaries(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "architecture", "runtime-mutation-kernel-adr.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{
		"Business Action is a named command boundary", "Direct CRUD", "MutationContext", "PlanCreate", "CommitMutationPlan",
		"Before Automation", "may not invoke Actions", "After Automation consumes a committed event", "explicit object allowlist",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("Mutation Kernel ADR missing %q", required)
		}
	}
}

func TestOneTimeMutationMigrationHasNoCompatibilityStatus(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "mutation_entrypoint_inventory_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"kernel_status": "parallel_path_review"`, `"kernel_status": "must_restrict"`, `"kernel_status": "partial_kernel"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("one-time migration inventory retains unfinished compatibility status %s", forbidden)
		}
	}
	document, err := os.ReadFile(filepath.Join("testdata", "runtime-mutation-one-time-migration-impact.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"不接受 `legacy`、fallback、双 validator、双 committer", "被新 owner 替代的代码必须物理删除", "repo fixture"} {
		if !strings.Contains(string(document), required) {
			t.Errorf("one-time migration impact inventory missing %q", required)
		}
	}
}

func TestRuntimeApplicationHasNoUnregisteredRecordRepositoryWriteEntrypoint(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "mutation_entrypoint_inventory_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory mutationEntrypointInventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, entry := range inventory.Entries {
		for _, source := range entry.SourceFiles {
			registered[filepath.Clean(source)] = true
		}
	}
	repositoryRoot := filepath.Join("..", "..")
	writeCalls := []string{".InsertRecord(", ".UpdateRecord(", ".DeleteRecord(", ".CommitRecordMutation(", ".CommitRecordMutationBatch(", ".CommitRecordMutationExecution(", ".UpdateRecordWhere("}
	for _, layer := range []string{"application", "transport"} {
		root := filepath.Join(repositoryRoot, "runtime", layer)
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			hasWrite := false
			for _, call := range writeCalls {
				if strings.Contains(string(content), call) {
					hasWrite = true
					break
				}
			}
			if !hasWrite {
				return nil
			}
			relative, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return err
			}
			if !registered[filepath.Clean(relative)] {
				t.Errorf("unregistered Runtime record write entrypoint: %s", filepath.ToSlash(relative))
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectCRUDUsesReviewedMutationKernelPlanning(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	read := func(relative string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for _, relative := range []string{
		"runtime/application/record/record_create_application_service.go",
		"runtime/application/record/record_update_application_service.go",
		"runtime/application/record/record_delete_application_service.go",
		"runtime/application/record/record_restore_application_service.go",
	} {
		content := read(relative)
		if !strings.Contains(content, "MutationKernel") || strings.Contains(content, ".Repository.CommitRecordMutation(") || strings.Contains(content, ".Repository.CommitRecordMutationBatch(") {
			t.Errorf("%s must plan and commit only through MutationKernel", relative)
		}
	}
}

func TestAutomationPhaseBoundaryHasNoPreCommitEffectFallback(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	read := func(relative string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	before := read("runtime/application/automation/automation_before_application_service.go")
	for _, required := range []string{"case \"derive_fields\"", "case \"assert\"", "backend.automation.before_instruction_unsupported"} {
		if !strings.Contains(before, required) {
			t.Errorf("before Automation pure executor missing %q", required)
		}
	}
	for _, forbidden := range []string{"InvokeAction", "RunWorkflow", "EmitEvent", "executionRepository", "appendAudit", "CommitMutation"} {
		if strings.Contains(before, forbidden) {
			t.Errorf("before Automation contains effectful owner %q", forbidden)
		}
	}
	validator := read("runtime/domain/automation/validation/automation_definition_validator.go") + read("runtime/domain/automation/validation/automation_authoring_fragment_validator.go")
	if !strings.Contains(validator, `instruction.Type != "derive_fields" && instruction.Type != "assert"`) {
		t.Fatal("Automation validator does not publish the pure before instruction set")
	}
	lifecycle := read("runtime/domain/automation/model/automation_schema.go") +
		read("runtime/application/automation/automation_application_service.go") +
		read("runtime/application/automation/automation_record_lifecycle_application_service.go")
	for _, required := range []string{"IdentityPolicy", "CorrelationID", "CausationID", "AutomationDepth", "VisitedRuleKeys", "backend.automation.identity_revoked", "backend.automation.recursion_detected", "backend.automation.max_depth_exceeded"} {
		if !strings.Contains(lifecycle, required) {
			t.Errorf("after Automation committed-event contract missing %q", required)
		}
	}
}

func TestWorkflowBusinessMutationUsesPublishedActionOnly(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	workflowRoot := filepath.Join(repositoryRoot, "runtime/application/workflow")
	forbidden := []string{"CommitRecordMutation(", "CommitRecordMutationBatch(", ".CreateRecord(", ".UpdateRecord(", ".DeleteRecord(", ".RestoreRecord("}
	err := filepath.WalkDir(workflowRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return walkErr
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, token := range forbidden {
			if strings.Contains(string(raw), token) {
				t.Errorf("Workflow owns forbidden Record mutation path %q in %s", token, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wiring, err := os.ReadFile(filepath.Join(repositoryRoot, "runtime/bootstrap/composition/workflow_dependencies_application_wiring.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"actionService.Invoke", "ActionSourceWorkflow", "ProcessID:", "NodeID:", "IdempotencyKey:"} {
		if !strings.Contains(string(wiring), required) {
			t.Errorf("Workflow published Action adapter missing %q", required)
		}
	}
}
