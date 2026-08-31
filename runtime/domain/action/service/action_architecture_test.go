package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBusinessActionArchitectureHasOneInvocationAndMutationExecutor(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	runtimeRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	production := readRuntimeProductionGo(t, runtimeRoot)

	assertSourceCount(t, production, "InvokeBusinessAction", 0)
	assertSourceCount(t, production, "func (s *ActionApplicationService) Invoke(", 1)
	assertSourceCount(t, production, "invocation.Source = source", 1)
	assertSourceCount(t, production, "invocation.Source = ActionSourceHTTP", 0)
	assertSourceCount(t, production, "func (e *BusinessHandlerExecutor) Execute(", 0)
	assertSourceCount(t, production, "func (e *BusinessHandlerExecutor) execute(", 1)
	assertSourceCount(t, production, "func (e *SystemOperationExecutor) Execute(", 0)
	assertSourceCount(t, production, "func (e *SystemOperationExecutor) execute(", 1)
	assertSourceCount(t, production, "BusinessHandlers.execute(", 1)
	assertSourceCount(t, production, "SystemOperations.execute(", 1)
	assertSourceCount(t, production, "binding.Handler.Invoke(", 1)
	assertSourceCount(t, production, "governedActionExecution{", 1)
	assertSourceCount(t, production, "unitOfWork.commit(", 1)
	assertSourceCount(t, production, "ExecuteActionWithContext", 0)
	assertSourceCount(t, production, "ExecuteObjectActionWithContext", 0)
	assertSourceCount(t, production, "func (s *RuntimeServices) executeObjectBusinessAction(", 0)
	assertSourceCount(t, production, "func (s *BusinessActionExecutor) executeObjectBusinessAction(", 0)
	assertSourceCount(t, production, "func (s *RuntimeServices) executeRecordBusinessAction(", 0)
	assertSourceCount(t, production, "func (s *BusinessActionExecutor) executeRecordBusinessAction(", 0)
	assertSourceCount(t, production, "func (r RecordStore) CommitRecordMutationBatch(", 1)

	for path, source := range production {
		if strings.Contains(source, "BusinessHandlers.execute(") && !strings.HasSuffix(filepath.ToSlash(path), "application/action/action_application_service.go") {
			t.Fatalf("Business Handler bypasses governed Action Application in %s", path)
		}
		if strings.Contains(source, "binding.Handler.Invoke(") && !strings.HasSuffix(filepath.ToSlash(path), "application/action/action_executor.go") {
			t.Fatalf("Business Handler invocation escaped its executor in %s", path)
		}
		if strings.Contains(source, "SystemOperations.execute(") && !strings.HasSuffix(filepath.ToSlash(path), "application/action/action_application_service.go") {
			t.Fatalf("System Operation bypasses governed Action Application in %s", path)
		}
		if strings.Contains(source, ".executeObjectBusinessAction(") {
			t.Fatalf("object Business Action executor bypass in %s", path)
		}
		if strings.Contains(source, ".executeRecordBusinessAction(") {
			t.Fatalf("record Business Action executor bypass in %s", path)
		}
	}
	assertGovernedHandlerPreflightOrder(t, production)
	for suffix, tokens := range map[string][]string{
		"bootstrap/composition/workflow_dependencies_application_wiring.go": {"actionService.Invoke(ctx, actionmodel.ActionSourceWorkflow", "ProcessID:", "NodeID:", "IdempotencyKey:"},
		"bootstrap/composition/automation_application_wiring.go":            {"actionService.Invoke(ctx, actionmodel.ActionSourceAutomation"},
		"bootstrap/composition/runtime_services_record_timer_adapter.go":    {"actionService.Invoke(ctx, actionmodel.ActionSourceRecordTimer", "IdempotencyKey: execution.IdempotencyKey"},
		"bootstrap/transport/agent_application_assembly.go":                 {"Actions.Invoke(ctx, actionmodel.ActionSourceAgent", "RequestID: request.RequestID", "IdempotencyKey: request.IdempotencyKey"},
		"transport/http/records/records_action_handler.go":                  {"actions.Invoke(r.Context(), actionmodel.ActionSourceHTTP", "IdempotencyKey: key"},
		"application/action/action_application_service.go":                  {"service.Invoke(ctx, actionmodel.ActionSourceBulk"},
	} {
		var entrypointSource string
		for path, content := range production {
			if strings.HasSuffix(filepath.ToSlash(path), suffix) {
				entrypointSource = content
			}
		}
		if entrypointSource == "" {
			t.Errorf("Action entrypoint source %s is missing", suffix)
			continue
		}
		for _, token := range tokens {
			if !strings.Contains(entrypointSource, token) {
				t.Errorf("Action entrypoint %s does not preserve canonical token %q", suffix, token)
			}
		}
	}
}

func assertGovernedHandlerPreflightOrder(t *testing.T, production map[string]string) {
	t.Helper()
	var source string
	for path, content := range production {
		if strings.HasSuffix(filepath.ToSlash(path), "application/action/action_application_service.go") {
			source = content
			break
		}
	}
	if source == "" {
		t.Fatal("Action Application source not found")
	}
	previous := -1
	for _, token := range []string{
		"Authorization.Validate(",
		"ActionNormalizePayload(",
		"actionValidateInvocationAssurance(",
		"s.dependencies.UnitOfWork.begin(",
		"governedActionExecution{",
	} {
		index := strings.Index(source, token)
		if index < 0 || index <= previous {
			t.Fatalf("Business Handler preflight order is not fixed at %q", token)
		}
		previous = index
	}
}

func readRuntimeProductionGo(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertSourceCount(t *testing.T, files map[string]string, pattern string, expected int) {
	t.Helper()
	count := 0
	for _, source := range files {
		count += strings.Count(source, pattern)
	}
	if count != expected {
		t.Fatalf("architecture contract %q expected %d production definitions, got %d", pattern, expected, count)
	}
}
