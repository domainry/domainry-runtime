package boundary

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type genericRuntimeDependency struct {
	ImportPath string
	Dir        string
	Standard   bool
	Module     *struct {
		Main bool
	}
}

func crossRepositoryPath(runtimeRepositoryRoot, relative string) string {
	clean := filepath.ToSlash(filepath.Clean(relative))
	for _, planeRoot := range []string{"internal", "examples", "scripts", "frontend", "conf"} {
		if clean == planeRoot {
			return filepath.Join(runtimeRepositoryRoot, "..", "domainry-plane", filepath.FromSlash(clean))
		}
	}
	for _, planePrefix := range []string{"internal/", "examples/", "scripts/", "frontend/", "conf/", "docs/adr/"} {
		if strings.HasPrefix(clean, planePrefix) {
			return filepath.Join(runtimeRepositoryRoot, "..", "domainry-plane", filepath.FromSlash(clean))
		}
	}
	return filepath.Join(runtimeRepositoryRoot, filepath.FromSlash(clean))
}

func TestPublicExtensionSDKsDoNotImportInternalOwners(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, owner := range []string{"pkg/runtimeext"} {
		root := filepath.Join(repositoryRoot, filepath.FromSlash(owner))
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}
			parsed, err := parser.ParseFile(
				token.NewFileSet(),
				path,
				nil,
				parser.ImportsOnly,
			)
			if err != nil {
				return err
			}
			for _, specification := range parsed.Imports {
				importPath, err := strconv.Unquote(specification.Path.Value)
				if err != nil {
					return err
				}
				if strings.Contains(importPath, "/internal/") {
					t.Errorf(
						"public extension SDK imports internal owner: %s -> %s",
						filepath.ToSlash(path),
						importPath,
					)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenericRuntimeBuildClosureExcludesProjectOwnedSource(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Clean(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "list", "-deps", "-json", "./runtime/cmd/server")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list generic Runtime build closure: %v\nstdout:\n%s\nstderr:\n%s", err, output, stderr.Bytes())
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	dependencies := []genericRuntimeDependency{}
	for {
		var dependency genericRuntimeDependency
		if err := decoder.Decode(&dependency); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode generic Runtime build closure: %v", err)
		}
		dependencies = append(dependencies, dependency)
	}
	if err := validateGenericRuntimeBuildClosure(repositoryRoot, dependencies); err != nil {
		t.Fatal(err)
	}

	projectDependency := genericRuntimeDependency{
		ImportPath: "example.com/project/actions",
		Dir:        filepath.Join(repositoryRoot, "actions"),
		Module:     &struct{ Main bool }{Main: true},
	}
	if err := validateGenericRuntimeBuildClosure(repositoryRoot, append(dependencies, projectDependency)); err == nil || !strings.Contains(err.Error(), "actions") {
		t.Fatalf("project-owned dependency was not rejected: %v", err)
	}
}

func validateGenericRuntimeBuildClosure(repositoryRoot string, dependencies []genericRuntimeDependency) error {
	allowedOwners := []string{"runtime", "pkg/runtimehost", "pkg/runtimeext"}
	mainFound := false
	for _, dependency := range dependencies {
		if dependency.ImportPath == "github.com/domainry/domainry-runtime/runtime/cmd/server" {
			mainFound = true
		}
		if dependency.Standard || dependency.Module == nil || !dependency.Module.Main {
			continue
		}
		relative, err := filepath.Rel(repositoryRoot, dependency.Dir)
		if err != nil {
			return fmt.Errorf("resolve generic Runtime dependency %s: %w", dependency.ImportPath, err)
		}
		relative = filepath.ToSlash(relative)
		allowed := false
		for _, owner := range allowedOwners {
			if relative == owner || strings.HasPrefix(relative, owner+"/") {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("generic Runtime build closure contains non-Runtime project owner %s at %s", dependency.ImportPath, relative)
		}
	}
	if !mainFound {
		return errors.New("generic Runtime build closure is missing runtime/cmd/server")
	}
	return nil
}

func TestActionApplicationKeepsCatalogAndExecutorsSeparated(t *testing.T) {
	descriptorType := reflect.TypeOf(actionapplication.SystemOperationDescriptor{})
	if _, exists := descriptorType.FieldByName("Execute"); exists {
		t.Fatal("System Operation Catalog descriptor must not own execution code")
	}
	dependenciesType := reflect.TypeOf(actionapplication.ActionApplicationDependencies{})
	want := map[string]reflect.Type{
		"Catalog":          reflect.TypeOf((*actionapplication.ActionCatalog)(nil)),
		"SystemOperations": reflect.TypeOf((*actionapplication.SystemOperationExecutor)(nil)),
		"BusinessHandlers": reflect.TypeOf((*actionapplication.BusinessHandlerExecutor)(nil)),
		"UnitOfWork":       reflect.TypeOf((*actionapplication.ActionUnitOfWorkManager)(nil)),
	}
	for fieldName, fieldType := range want {
		field, exists := dependenciesType.FieldByName(fieldName)
		if !exists || field.Type != fieldType {
			t.Errorf("Action Application dependency %s has type %v, want %v", fieldName, field.Type, fieldType)
		}
	}

	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	compositionPath := filepath.Join(repositoryRoot, "runtime", "bootstrap", "composition", "action_application_wiring.go")
	raw, err := os.ReadFile(compositionPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, forbidden := range []string{"simpleSystemOperationExecutor", "switch kind"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("composition reintroduced System Operation implementation %q", forbidden)
		}
	}
	for _, required := range []string{"NewRuntimeSystemOperationCatalog", "NewRuntimeSystemOperationExecutor", "NewBusinessHandlerExecutor", "NewActionApplication"} {
		if !strings.Contains(content, required) {
			t.Errorf("composition no longer assembles the explicit Action boundary: missing %q", required)
		}
	}
}

func TestActionExecutorsReturnCanonicalPlansToRuntimeOwnedUnitOfWork(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	requiredByFile := map[string][]string{
		"runtime/application/action/action_unit_of_work.go": {
			"type ActionUnitOfWorkManager struct", "BeginTransaction(ctx)", "CommitTransaction(ctx", "transaction.Context(ctx)", "transaction.RollBack(ctx)",
			"acquireSynchronousConnectorCall", "activeSynchronousConnectorCalls",
		},
		"runtime/domain/action/runtime/action_execution_runtime.go": {
			"transaction.Commit(ctx", "actionExecutionCompletion(claim, resultMap, audits)",
		},
		"runtime/application/integration/integration_application_sync_call.go": {
			"execution.AcquireSynchronousConnectorCall(runtimeext.ActionConnectorCapability{", "defer lease.Release()", "actionConnectorPrincipal(execution)", "ConnectorActionSideEffectOutboxErrorCode",
		},
		"runtime/application/action/action_system_operation_executor.go": {
			"PlanCreateMutation", "PlanUpdateMutation", "PlanDeleteMutation", "PlanRestoreMutation", "PlanConditionalUpdate", "CanonicalCommit()",
		},
		"runtime/bootstrap/composition/action_application_wiring.go": {
			"pipelineTransitions.Plan(", "GetRecordForUpdate", "ValidateDurableIntent", "NewActionUnitOfWorkManager(records.ActionExecutionRuntime)",
		},
		"runtime/infrastructure/persistence/database/action/action_business_execution_store.go": {
			"BeginExecutionTransaction", "profile.BeginWrite(ctx, r.db)", "WithActionExecutionTransaction",
		},
		"runtime/infrastructure/persistence/database/record/record_query_store.go": {
			"actionExecutionTransaction(ctx)", "RecordQueryLockForUpdate", "ApplyClaimLock",
		},
		"internal/controlplane/domaincodegen/templates/query_object.go.tmpl": {
			"GetForUpdate", "runtimeext.QueryGetForUpdate",
		},
		"internal/controlplane/domaincodegen/templates/connector_runtime.go.tmpl": {
			"execution.Phase() != runtimeext.ExecutionPhasePrewrite", "ConnectorCallAfterWriteErrorCode",
		},
	}
	for relative, required := range requiredByFile {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		for _, token := range required {
			if !strings.Contains(content, token) {
				t.Errorf("Action UoW proof %s is missing %q", relative, token)
			}
		}
	}

	systemSource, err := os.ReadFile(filepath.Join(repositoryRoot, "runtime", "application", "action", "action_system_operation_executor.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CreateRecord", "UpdateRecord", "DeleteRecord", "RestoreRecord", "ConditionalUpdateRecord"} {
		if strings.Contains(string(systemSource), forbidden) {
			t.Errorf("System Operation regained a self-committing Record port %q", forbidden)
		}
	}
	wiring, err := os.ReadFile(filepath.Join(repositoryRoot, "runtime", "bootstrap", "composition", "action_application_wiring.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wiring), "pipelineTransitions.Execute(") {
		t.Fatal("Pipeline System Operation bypasses Action UoW with a self-committing Execute call")
	}
	uowSource, err := os.ReadFile(filepath.Join(repositoryRoot, "runtime", "application", "action", "action_unit_of_work.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(uowSource), "CommitMutations(ctx") {
		t.Fatal("Action UoW deferred transaction opening until final CommitMutations")
	}
	for relative, forbidden := range map[string][]string{
		"runtime/domain/action/runtime/action_execution_runtime.go":                             {"CommitMutations("},
		"runtime/domain/action/contract/action_execution_claim.go":                              {"CommitExecution("},
		"runtime/infrastructure/persistence/database/action/action_business_execution_store.go": {"CommitExecution("},
	} {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(raw), token) {
				t.Errorf("duplicate Action side-effect commit path %q reentered %s", token, relative)
			}
		}
	}
}

func TestActionMutationAuditDurableIntentAndReceiptShareOneTransaction(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	requiredByFile := map[string][]string{
		"runtime/application/action/action_application_service.go": {
			"BuildSuccess", "receiptResult, err := actionReceiptResult(result)",
			"unitOfWork.commit(ctx, receiptResult, executed.Commits, []auditmodel.AuditEvent{auditEvent})",
		},
		"runtime/application/action/action_executor_mutation.go": {
			"StageDurableIntent", "canonicalCommits()", "commits[len(commits)-1].Outbox = append",
		},
		"runtime/application/action/action_unit_of_work.go": {
			"CommitTransaction(ctx, u.transaction, u.claim, result, commits, audits)",
		},
		"runtime/domain/action/model/action_execution.go": {
			"AuditEvents []auditmodel.AuditEvent",
		},
		"runtime/infrastructure/persistence/database/action/action_business_execution_store.go": {
			"ApplyRecordMutationTx", "for _, evidence := range completion.AuditEvents", "ApplyAuditTx", "business_action_executions", "commitSQL(ctx)",
		},
	}
	for relative, required := range requiredByFile {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range required {
			if !strings.Contains(string(raw), token) {
				t.Errorf("Action atomic fact proof %s is missing %q", relative, token)
			}
		}
	}

	applicationSource, err := os.ReadFile(filepath.Join(repositoryRoot, "runtime", "application", "action", "action_application_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Audit.Success", "dependencies.Audit.Success"} {
		if strings.Contains(string(applicationSource), forbidden) {
			t.Errorf("Action success audit escaped the transaction through %q", forbidden)
		}
	}
}

func TestBookClassAtomicFailureProofCoversEveryCommittedFactStage(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(
		repositoryRoot,
		"runtime", "infrastructure", "persistence", "database", "action",
		"action_book_class_rollback_test.go",
	)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{
		"TestBookClassRollsBackEveryFactWhenAnyAtomicStageFails",
		`"class_mutation"`,
		`"booking_mutation"`,
		`"class_audit"`,
		`"booking_audit"`,
		`"outbox"`,
		`"action_audit"`,
		`"receipt"`,
		`storedClass.Data["remaining_capacity"] != int64(1)`,
		`records.GetRecord(t.Context(), "workspace-a", classBooking, "booking-1")`,
		`"_audit_events":`,
		`"runtime_publication_outbox":`,
		`receipt.Status != string(idempotency.StatusProcessing)`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("book_class atomic failure proof is missing %q", required)
		}
	}
}

func TestProjectConnectorDeliveryIsBoundToCommittedActionOutbox(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	requiredByFile := map[string][]string{
		filepath.Join(
			repositoryRoot,
			"runtime", "application", "integration",
			"integration_runtime_orchestration_application_service.go",
		): {
			"func (s *IntegrationApplicationService) RegisterProviderIntegrationOutboxSenders()",
			"s.registry.AdapterReady(connector)",
			"s.RegisterIntegrationOutboxSender(connector.Key, s)",
		},
	}
	for path, required := range requiredByFile {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		for _, marker := range required {
			if !strings.Contains(content, marker) {
				t.Errorf("%s is missing committed Action Outbox marker %q", path, marker)
			}
		}
	}
}

func TestProjectConnectorReliabilityPreservesUnknownReceiptAndWebhookConvergence(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	sdkRoot := connectorSDKRoot(t, repositoryRoot)
	requiredByFile := map[string][]string{
		filepath.Join(sdkRoot, "contract.go"): {
			`const ContractVersion = "connector-contract-v10"`,
		},
		filepath.Join(sdkRoot, "operation.go"): {
			"ResponseRef: result.ResponseRef, SecretUpdates: cloneStrings(result.SecretUpdates)",
		},
	}
	for path, required := range requiredByFile {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		for _, marker := range required {
			if !strings.Contains(content, marker) {
				t.Errorf("%s is missing project Connector reliability marker %q", path, marker)
			}
		}
	}
}

func TestBusinessHandlerOutputIsStrictlyValidatedBeforeCommit(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	requiredByFile := map[string][]string{
		"internal/controlplane/domaincodegen/templates/capabilities_binding.go.tmpl": {
			"validateActionOutput(encoded", "reflect.TypeOf((*Output)(nil)).Elem()",
		},
		"internal/controlplane/domaincodegen/templates/capabilities_runtime.go.tmpl": {
			"func validateActionOutput", "rejectDuplicateActionJSONFields", "validateRequiredActionJSON", "DisallowUnknownFields", "ActionOutputInvalidErrorCode",
		},
		"runtime/application/action/action_executor.go": {
			"decodeBusinessHandlerOutput(rawOutput)", "backend.action.handler_output_invalid",
		},
		"runtime/application/action/action_handler_output.go": {
			"Business Handler output must be a JSON object", "contains duplicate field", "decoder.UseNumber()",
		},
		"runtime/application/action/action_application_service.go": {
			"unitOfWork.rollBack(ctx)", "unitOfWork.commit(ctx",
		},
	}
	for relative, required := range requiredByFile {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range required {
			if !strings.Contains(string(raw), token) {
				t.Errorf("strict Action output proof %s is missing %q", relative, token)
			}
		}
	}

	executor, err := os.ReadFile(filepath.Join(repositoryRoot, "runtime", "application", "action", "action_executor.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`output = map[string]any{"value": decoded}`, `string(rawOutput) != "null"`} {
		if strings.Contains(string(executor), forbidden) {
			t.Errorf("Business Handler output retained permissive fallback %q", forbidden)
		}
	}
	executorSource := string(executor)
	if outputIndex, commitIndex := strings.Index(executorSource, "decodeBusinessHandlerOutput(rawOutput)"), strings.Index(executorSource, "session.canonicalCommits()"); outputIndex < 0 || commitIndex < 0 || outputIndex >= commitIndex {
		t.Fatal("Business Handler output is not validated before canonical commit materialization")
	}
}

func TestActionFailureModesKeepDistinctRollbackAndUnknownCommitSemantics(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	requiredByFile := map[string][]string{
		"runtime/application/action/action_application_service.go": {
			"if recovered := recover()", "backend.action.handler_panicked", "unitOfWork.rollBack(context.WithoutCancel(ctx))",
			"actionReceiptResult(result)", "backend.action.result_invalid", "failOwnedInvocation",
		},
		"runtime/application/action/action_invocation_application_service.go": {
			"backend.action.cancelled", "backend.action.timeout", "StableConflictCode", "StableTransientCode", "TransactionCommitUnknownCode",
		},
		"runtime/domain/action/runtime/action_execution_runtime.go": {
			"backend.action.cancelled", "backend.action.timeout", "StableConflictCode", "StableTransientCode", "TransactionCommitUnknownCode",
			"StatusFailedTerminal", "replayActionExecutionFailure", "Retryable: result.Retryable",
		},
		"runtime/application/action/action_unit_of_work.go": {
			"if !mutation.IsTransactionCommitUnknown(err)", "u.phases.rollBack()",
		},
		"runtime/infrastructure/persistence/database/action/action_business_execution_store.go": {
			"database.MutationTransactionError", "mutation.TransactionCommitUnknown", "t.commitSQL(ctx)",
			"StatusFailedRetryable", "completion.Retryable",
		},
	}
	for relative, required := range requiredByFile {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range required {
			if !strings.Contains(string(raw), token) {
				t.Errorf("Action failure semantics %s is missing %q", relative, token)
			}
		}
	}
}

func TestActionExecutionPhaseTransitionsRemainRuntimeOwned(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	actionRoot := filepath.Join(repositoryRoot, "runtime", "application", "action")
	phaseOwner := filepath.Join(actionRoot, "action_execution_phase.go")
	ownerRaw, err := os.ReadFile(phaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	owner := string(ownerRaw)
	for _, token := range []string{
		"ExecutionPhasePrewrite", "ExecutionPhaseWriting", "ExecutionPhaseCommitted", "ExecutionPhaseRolledBack",
		"func (m *actionExecutionPhaseMachine) transition(",
		"func (m *actionExecutionPhaseMachine) allowsSynchronousConnectorCall() bool",
	} {
		if !strings.Contains(owner, token) {
			t.Errorf("Action phase owner is missing %q", token)
		}
	}

	err = filepath.WalkDir(actionRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || path == phaseOwner {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{"ExecutionPhasePrewrite", "ExecutionPhaseWriting", "ExecutionPhaseCommitted", "ExecutionPhaseRolledBack", ".phase ="} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("Action phase transition escaped Runtime phase owner in %s: %q", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	requiredByFile := map[string][]string{
		"action_executor_mutation.go":   {"e.unitOfWork.beginWriting(ctx)"},
		"action_unit_of_work.go":        {"u.phases.beginWriting()", "u.phases.commit()", "u.phases.rollBack()"},
		"action_application_service.go": {"unitOfWork.rollBack(ctx)"},
	}
	for name, required := range requiredByFile {
		raw, readErr := os.ReadFile(filepath.Join(actionRoot, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, token := range required {
			if !strings.Contains(string(raw), token) {
				t.Errorf("Action phase integration %s is missing %q", name, token)
			}
		}
	}
}

func TestPublishedActionOwnerIsDerivedFromGeneratedCapabilities(t *testing.T) {
	handlerDescriptor := reflect.TypeOf(runtimeext.HandlerDescriptor{})
	for _, fieldName := range []string{"InputType", "OutputType", "InputContractSHA256", "OutputContractSHA256"} {
		field, exists := handlerDescriptor.FieldByName(fieldName)
		if !exists || field.Type.Kind() != reflect.String {
			t.Fatalf("HandlerDescriptor %s type=%v exists=%v", fieldName, field.Type, exists)
		}
	}
	field, exists := handlerDescriptor.FieldByName("ObjectCapabilities")
	if !exists || field.Type != reflect.TypeOf([]runtimeext.ActionObjectCapability{}) {
		t.Fatalf("HandlerDescriptor ObjectCapabilities type=%v exists=%v", field.Type, exists)
	}
	field, exists = handlerDescriptor.FieldByName("ConnectorCapabilities")
	if !exists || field.Type != reflect.TypeOf([]runtimeext.ActionConnectorCapability{}) {
		t.Fatalf("HandlerDescriptor ConnectorCapabilities type=%v exists=%v", field.Type, exists)
	}
	systemDescriptor := reflect.TypeOf(actionapplication.SystemOperationDescriptor{})
	field, exists = systemDescriptor.FieldByName("WriteOperation")
	if !exists || field.Type.Kind() != reflect.String {
		t.Fatalf("SystemOperationDescriptor WriteOperation type=%v exists=%v", field.Type, exists)
	}

	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	requiredByFile := map[string][]string{
		"runtime/application/action/action_owner_resolution.go": {
			"effectSetFromSystemCapability", "effectSetFromHandlerCapabilities", "effectWriteCapabilityMatches", "business write set does not match its published execution capability",
		},
		"internal/controlplane/domaincodegen/templates/capabilities_binding.go.tmpl": {
			"ObjectCapabilities: []runtimeext.ActionObjectCapability", "ConnectorCapabilities: []runtimeext.ActionConnectorCapability", "ObjectKey:", "Operations:",
		},
	}
	for relative, required := range requiredByFile {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range required {
			if !strings.Contains(string(raw), token) {
				t.Errorf("published owner proof %s is missing %q", relative, token)
			}
		}
	}
}

func TestRuntimeValidatesActionRegistryCatalogBeforeSeedsAndHTTPReadiness(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	raw, err := os.ReadFile(filepath.Join(repositoryRoot, "runtime", "bootstrap", "runtime", "startup.go"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	assembly := strings.Index(content, "assembleRuntimeServices(")
	readiness := strings.Index(content, "validateRuntimeActionReadiness(records.Applications().Actions)")
	seeds := strings.Index(content, "synchronizeRuntimeSeeds(")
	http := strings.Index(content, "return BindHTTP(ctx, runtime)")
	if assembly < 0 || readiness <= assembly || seeds <= readiness || http <= seeds {
		t.Fatalf("Action readiness order assembly=%d readiness=%d seeds=%d http=%d", assembly, readiness, seeds, http)
	}
	for _, required := range []string{"CatalogValidationErrors()", "errors.Join(validationErrors...)", "mustCompleteRuntimeStartup(validateRuntimeActionReadiness"} {
		if !strings.Contains(content, required) {
			t.Errorf("Runtime Action readiness is missing %q", required)
		}
	}
}

func TestBusinessHandlerExecutionIdentityTracesRuntimeProjectSnapshotHandlerAndReceipt(t *testing.T) {
	identity := reflect.TypeOf(runtimeext.ExecutionIdentity{})
	for _, fieldName := range []string{"RuntimeRevision", "ProjectRevision", "ApplicationSchemaRevision", "HandlerRevision", "ReceiptID"} {
		field, exists := identity.FieldByName(fieldName)
		if !exists || field.Type.Kind() != reflect.String {
			t.Fatalf("ExecutionIdentity %s type=%v exists=%v", fieldName, field.Type, exists)
		}
	}
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	requiredByFile := map[string][]string{
		"runtime/bootstrap/runtime/service_assembly.go": {
			"ActionRuntimeRevision:", "cfg.RuntimeVersion", "ActionProjectRevision:", "projectRevision", "ActionMetadataRevision:", "metadataRevision",
		},
		"runtime/bootstrap/runtime/notification_startup_bindings.go": {
			"manifest.GeneratedDomainSDK.ArtifactSHA256", "manifest.GeneratedDomainSDK.ApplicationSchemaSnapshotSHA256",
		},
		"runtime/bootstrap/composition/action_application_wiring.go": {
			"ResolveMetadataRevision:", "SnapshotRevision(ctx", "resolve Action execution identity",
		},
		"runtime/application/action/action_executor_identity.go": {
			"ReceiptID: executionID", "RuntimeRevision: e.dependencies.RuntimeRevision", "ApplicationSchemaRevision: metadataRevision",
			"ProjectRevision: e.dependencies.ProjectRevision", "HandlerRevision: descriptor.HandlerRevision", "backend.action.execution_identity_incomplete",
		},
	}
	for relative, required := range requiredByFile {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range required {
			if !strings.Contains(string(raw), token) {
				t.Errorf("Action execution trace %s is missing %q", relative, token)
			}
		}
	}
}

func TestConnectorPublishesOneAdapterCallEnvelopeWithoutOldAlias(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	packageRoot := connectorSDKRoot(t, repositoryRoot)
	var source strings.Builder
	err := filepath.WalkDir(packageRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		source.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	content := source.String()
	for _, required := range []string{
		"type Adapter interface", "type CallRequest struct", "type CallResult struct", "Call(context.Context, CallRequest) (CallResult, error)",
		"type CallOperation[", "type EnqueueOperation[", "type StartOperation[", "func BindCall[", "func BindEnqueueDelivery[", "func BindStartOperationDelivery[",
		"type ProviderSchema struct", "type ConfigField struct", "type SecretField struct", "ConfigFields", "[]ConfigField", "SecretFields", "[]SecretField",
		"type ReliabilityContract struct", "type Reconciler interface", "type ReconcileRequest struct", "type ReconcileResult struct",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("connector public contract is missing %q", required)
		}
	}
	for _, forbidden := range []string{"type ProviderAdapter interface", "type ProviderAdapter =", "ProviderAdapter.Descriptor", "type Operation[", "func Bind[", "definitionmodel.FieldSchema", "runtime"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("connector retained old public Adapter name %q", forbidden)
		}
	}
}

func connectorSDKRoot(t *testing.T, repositoryRoot string) string {
	t.Helper()
	command := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/domainry/domainry-connector-sdk")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve Connector SDK module: %v\n%s", err, output)
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		t.Fatal("Connector SDK module directory is empty")
	}
	return root
}

func TestConnectorProviderRegistryRemainsOneFrozenStateSource(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	registryPath := filepath.Join(repositoryRoot, "runtime", "application", "integration", "integration_registry.go")
	registryRaw, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	registrySource := string(registryRaw)
	for _, required := range []string{
		"connectorProviders *connector.Registry",
		"!connectorProviders.Frozen()",
		"r.connectorProviders.Provider(connectorKey, providerKey)",
	} {
		if !strings.Contains(registrySource, required) {
			t.Errorf("unified Connector Provider Registry is missing %q", required)
		}
	}
	for _, forbidden := range []string{"legacyAdapters", "legacyProviderSchemas", "RegisterProviderAdapter"} {
		if strings.Contains(registrySource, forbidden) {
			t.Errorf("unified Connector Provider Registry retained legacy state or registration %q", forbidden)
		}
	}

	bridgePath := filepath.Join(repositoryRoot, "runtime", "application", "integration", "integration_application_registry.go")
	bridgeRaw, err := os.ReadFile(bridgePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"RegisterConnectorProviders", "RegisterProviderIntegrationAdapter", "providers.Providers()"} {
		if strings.Contains(string(bridgeRaw), forbidden) {
			t.Errorf("public providers are still copied into internal Registry state through %q", forbidden)
		}
	}

	compositionPath := filepath.Join(repositoryRoot, "runtime", "bootstrap", "composition", "runtime_services_initialization.go")
	compositionRaw, err := os.ReadFile(compositionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compositionRaw), "newRuntimeConnectorCatalog(manifest.Integrations)") {
		t.Fatal("composition does not build the Runtime-owned connector requirement catalog")
	}
	if strings.Contains(string(compositionRaw), "deps.ConnectorProviders") {
		t.Fatal("composition must not copy provider adapters into the Runtime-owned connector requirement catalog")
	}

	for relative, forbidden := range map[string][]string{
		"runtime/application/integration/integration_application_catalog.go": {
			"if len(connector.Providers) == 0",
		},
		"runtime/application/integration/integration_application_connection_provider.go": {
			"legacyConnectionProvider(",
			"delete(connection.Config, \"provider\")",
		},
		"runtime/application/integration/integration_connection_validation.go": {
			"legacy := strings.TrimSpace(connector.Provider)",
		},
		"runtime/application/integration/integration_operation_support.go": {
			"if len(connector.Providers) == 0",
		},
		"runtime/application/integration/integration_registry.go": {
			"for _, providerKey := range connectorProviderKeys(connector)",
		},
		"runtime/application/integration/manifest_connection_installation.go": {
			"strings.TrimSpace(connector.Provider)",
		},
		"runtime/domain/manifest/validation/manifest_validator_refs.go": {
			"return strings.TrimSpace(connector.Provider) == providerKey",
		},
		"internal/controlplane/authoring/validation/integration_validation.go": {
			"if len(connector.Providers) == 0",
		},
		"internal/controlplane/authoring/definition/connector_detail.go": {
			"len(providers) == 0 && strings.TrimSpace(connector.Provider)",
		},
		"internal/controlplane/authoring/definition/connector_summary_catalog.go": {
			"len(connector.Providers) > 0 || strings.TrimSpace(connector.Provider)",
		},
	} {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(raw), token) {
				t.Errorf("Connector Provider family fallback %q reentered %s", token, relative)
			}
		}
	}
}

func TestIntegrationAdapterExecutionHasNoInvokeFallback(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	paths := []string{
		"runtime/application/integration/integration_application_adapter_resolution.go",
		"runtime/domain/integration/contract/integration_connector_adapter.go",
	}
	for _, relative := range paths {
		raw, err := os.ReadFile(crossRepositoryPath(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"integrationcontract.Invoker", "type Invoker", "type ProviderAdapter", ".Invoke(ctx"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("%s retained legacy Connector execution surface %q", relative, forbidden)
			}
		}
	}

	connectorsRoot := filepath.Join(repositoryRoot, "runtime", "infrastructure", "connectors")
	if _, err := os.Stat(connectorsRoot); !os.IsNotExist(err) {
		t.Fatalf("retired Connector implementation root still exists: %v", err)
	}
}

func TestActionSchemaHasNoFreeFormConfigOwnership(t *testing.T) {
	actionType := reflect.TypeOf(definitionmodel.ActionSchema{})
	if _, exists := actionType.FieldByName("Config"); exists {
		t.Fatal("ActionSchema reintroduced free-form Config ownership")
	}
	if _, exists := actionType.FieldByName("RetiredConfig"); exists {
		t.Fatal("ActionSchema retained the Action Config tombstone")
	}
}

func TestActionStepDSLDoesNotReenterProductionSurfaces(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	forbidden := []string{
		"DomainBlueprintActionStep",
		"ActionPortStepHandler",
		"ActionStepDependencies",
		"ActionStepResult",
		"ActionRenderContext",
		"ActionApplyConfiguredCreates",
		"ActionAggregateApplicationService",
		"ActionSideEffectApplicationService",
		"actionApplySideEffects",
		`config["steps"]`,
		"config.steps",
		"/metadata/definitions/action/{resourceKey}/simulate",
		"ActionWorkbench",
		"action.definition.editor.v1",
		"integration_call_step",
		"backend.action.step_key_invalid",
		"backend.action.step_type_unsupported",
	}
	productionRoots := []string{
		"internal",
		"pkg",
		"examples",
		"scripts",
		"frontend/domainry-admin/src",
		"conf",
	}
	for _, relativeRoot := range productionRoots {
		walkProductionSourceFiles(t, crossRepositoryPath(repositoryRoot, relativeRoot), func(path string, raw []byte) {
			for _, token := range forbidden {
				if strings.Contains(string(raw), token) {
					t.Errorf("retired Action JSON DSL token %q re-entered %s", token, filepath.ToSlash(path))
				}
			}
		})
	}

	integrationRoot := filepath.Join(repositoryRoot, "runtime", "application", "integration")
	walkProductionSourceFiles(t, integrationRoot, func(path string, raw []byte) {
		for _, token := range []string{"StepKey", "StepMode", `"step_key"`, `"step_mode"`} {
			if strings.Contains(string(raw), token) {
				t.Errorf("retired Connector Action Step invocation token %q re-entered %s", token, filepath.ToSlash(path))
			}
		}
	})

	metricsPath := filepath.Join(repositoryRoot, "runtime", "domain", "deployment", "projection")
	walkProductionSourceFiles(t, metricsPath, func(path string, raw []byte) {
		if strings.Contains(string(raw), "step_duration_ms") {
			t.Errorf("retired Action Step metric re-entered %s", filepath.ToSlash(path))
		}
	})
}

func TestIndustrySpecificActionsDoNotReenterRuntimeProduction(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	runtimeRoot := filepath.Join(repositoryRoot, "runtime")
	forbidden := []string{
		"ActionInvoiceSettlementApplicationService",
		"ActionApplyStockReplenishment",
		"ActionApplyContactPrimaryExclusivity",
		`action.Config["invoice_settlement"]`,
		`action.Key != "stock_item.create_replenishment"`,
		`schema.Key != "contact.mark_primary"`,
		"ActionProjectRecordForRendering",
		"ActionFailureAuditMetadata",
		"supplier_promise",
		"warehouse_operator",
		"finance_user",
		"inventory_admin",
	}
	walkProductionSourceFiles(t, runtimeRoot, func(path string, raw []byte) {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go", ".tmpl", ".sql":
		default:
			return
		}
		for _, token := range forbidden {
			if strings.Contains(string(raw), token) {
				t.Errorf("industry-specific Action token %q re-entered %s", token, filepath.ToSlash(path))
			}
		}
	})
}

func walkProductionSourceFiles(t *testing.T, root string, inspect func(path string, raw []byte)) {
	t.Helper()
	allowedExtensions := map[string]struct{}{
		".go": {}, ".tmpl": {}, ".ts": {}, ".tsx": {}, ".js": {}, ".jsx": {},
		".json": {}, ".yaml": {}, ".yml": {}, ".toml": {}, ".sh": {}, ".py": {}, ".sql": {},
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", "testdata", "__tests__":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		name := entry.Name()
		if strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
			return nil
		}
		if _, allowed := allowedExtensions[strings.ToLower(filepath.Ext(name))]; !allowed {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		inspect(path, raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

const sourceOwnedBusinessExtensionInventoryVersion = "runtime-source-owned-business-extension-inventory-v1"

type sourceOwnedBusinessExtensionInventory struct {
	SchemaVersion       string                                      `json:"schema_version"`
	TargetADR           string                                      `json:"target_adr"`
	AllowedDispositions []string                                    `json:"allowed_dispositions"`
	Entries             []sourceOwnedBusinessExtensionInventoryItem `json:"entries"`
}

type sourceOwnedBusinessExtensionInventoryItem struct {
	ID               string                                          `json:"id"`
	Category         string                                          `json:"category"`
	Disposition      string                                          `json:"disposition"`
	CurrentOwner     string                                          `json:"current_owner"`
	ReplacementOwner string                                          `json:"replacement_owner"`
	Replacement      string                                          `json:"replacement"`
	Evidence         []sourceOwnedBusinessExtensionInventoryEvidence `json:"evidence"`
}

type sourceOwnedBusinessExtensionInventoryEvidence struct {
	Path     string `json:"path"`
	Token    string `json:"token"`
	Presence string `json:"presence,omitempty"`
}

func TestSourceOwnedBusinessExtensionADRLocksTheCleanCutBoundary(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	path := crossRepositoryPath(repositoryRoot, "docs/adr/0003-source-owned-business-extension-boundary.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	required := []string{
		"Status: Accepted",
		"Runtime is published as open, versioned Go modules",
		"System Operation",
		"Business Handler",
		"fails publication or readiness",
		"Runtime never guesses and never falls back",
		"Users do not maintain `execution_kind`",
		"<ActionName>Capabilities",
		"runtimeext",
		"connector",
		"runtimehost",
		"semantic versioning",
		"trusted project source in self-hosted or private deployments",
		"WASM extensions",
		"Go `plugin` loading",
		"sidecar transactions",
		"Runtime scripts",
		"project-owned SQL",
	}
	for _, fragment := range required {
		if !strings.Contains(content, fragment) {
			t.Errorf("source-owned business extension ADR is incomplete: missing %q", fragment)
		}
	}
}

func TestSourceOwnedBusinessExtensionInventoryMatchesCurrentSource(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	inventoryPath := filepath.Join(repositoryRoot, "runtime", "boundary", "testdata", "runtime-source-owned-business-extension-inventory.json")
	raw, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	var inventory sourceOwnedBusinessExtensionInventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatalf("decode source-owned business extension inventory: %v", err)
	}
	if inventory.SchemaVersion != sourceOwnedBusinessExtensionInventoryVersion {
		t.Fatalf("inventory schema_version = %q, want %q", inventory.SchemaVersion, sourceOwnedBusinessExtensionInventoryVersion)
	}
	if inventory.TargetADR != "docs/adr/0003-source-owned-business-extension-boundary.md" {
		t.Fatalf("inventory target_adr = %q", inventory.TargetADR)
	}
	if got := strings.Join(inventory.AllowedDispositions, ","); got != "keep,move-owner,replace,delete" {
		t.Fatalf("inventory allowed_dispositions = %q", got)
	}

	allowedCategories := map[string]bool{
		"action_step":             true,
		"config_dsl":              true,
		"industry_specialization": true,
		"connector_legacy":        true,
		"runtime_kernel":          true,
	}
	allowedDispositions := map[string]bool{"keep": true, "move-owner": true, "replace": true, "delete": true}
	requiredIDs := map[string]bool{
		"controlplane.action-step-authoring":            false,
		"runtime.action-step-dispatcher":                false,
		"runtime.action-step-service-bag":               false,
		"runtime.configured-create-config":              false,
		"runtime.aggregate-config":                      false,
		"runtime.side-effect-orchestration-config":      false,
		"runtime.invoice-settlement":                    false,
		"runtime.stock-replenishment":                   false,
		"runtime.contact-primary":                       false,
		"connector.single-key-registration":             false,
		"connector.application-single-key-registration": false,
		"connector.adapter-key-fallback":                false,
		"connector.manual-builtin-registration":         false,
		"runtime.record-query-ast":                      false,
		"runtime.record-store-sql-dialect":              false,
		"runtime.mutation-kernel":                       false,
		"runtime.unit-of-work":                          false,
		"runtime.conditional-update-cas":                false,
		"runtime.database-unique-constraint":            false,
		"runtime.workflow":                              false,
		"runtime.automation":                            false,
		"runtime.state-machine":                         false,
		"runtime.scheduler":                             false,
		"runtime.durable-intent-outbox":                 false,
		"runtime.connector-lifecycle":                   false,
		"runtime.generic-action-http-routes":            false,
		"runtime.action-governance-chain":               false,
	}
	seen := map[string]bool{}
	categoryCounts := map[string]int{}
	for index, entry := range inventory.Entries {
		if strings.TrimSpace(entry.ID) == "" || seen[entry.ID] {
			t.Errorf("entries[%d] has empty or duplicate id %q", index, entry.ID)
			continue
		}
		seen[entry.ID] = true
		if _, required := requiredIDs[entry.ID]; required {
			requiredIDs[entry.ID] = true
		}
		if !allowedCategories[entry.Category] {
			t.Errorf("entry %s has unsupported category %q", entry.ID, entry.Category)
		}
		categoryCounts[entry.Category]++
		if !allowedDispositions[entry.Disposition] {
			t.Errorf("entry %s has unsupported disposition %q", entry.ID, entry.Disposition)
		}
		if strings.TrimSpace(entry.CurrentOwner) == "" || strings.TrimSpace(entry.ReplacementOwner) == "" || strings.TrimSpace(entry.Replacement) == "" {
			t.Errorf("entry %s must name current owner, replacement owner and replacement", entry.ID)
		}
		if len(entry.Evidence) == 0 {
			t.Errorf("entry %s has no current-source evidence", entry.ID)
		}
		for evidenceIndex, evidence := range entry.Evidence {
			if filepath.IsAbs(evidence.Path) || strings.Contains(filepath.ToSlash(evidence.Path), "../") {
				t.Errorf("entry %s evidence[%d] path must be repository-relative: %q", entry.ID, evidenceIndex, evidence.Path)
				continue
			}
			evidenceRaw, readErr := os.ReadFile(crossRepositoryPath(repositoryRoot, evidence.Path))
			if readErr != nil {
				t.Errorf("entry %s evidence[%d]: %v", entry.ID, evidenceIndex, readErr)
				continue
			}
			if strings.TrimSpace(evidence.Token) == "" {
				t.Errorf("entry %s evidence[%d] token is empty", entry.ID, evidenceIndex)
				continue
			}
			present := strings.Contains(string(evidenceRaw), evidence.Token)
			switch evidence.Presence {
			case "", "present":
				if !present {
					t.Errorf("entry %s evidence[%d] token %q is absent from %s", entry.ID, evidenceIndex, evidence.Token, evidence.Path)
				}
			case "absent":
				if present {
					t.Errorf("entry %s evidence[%d] deleted token %q remains in %s", entry.ID, evidenceIndex, evidence.Token, evidence.Path)
				}
			default:
				t.Errorf("entry %s evidence[%d] has unsupported presence %q", entry.ID, evidenceIndex, evidence.Presence)
			}
		}
	}
	for category := range allowedCategories {
		if categoryCounts[category] == 0 {
			t.Errorf("inventory has no %s entries", category)
		}
	}
	for id, present := range requiredIDs {
		if !present {
			t.Errorf("inventory is missing required entry %s", id)
		}
	}
}
