package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPHandlersDoNotAccessIdempotencyStores(t *testing.T) {
	transportRoot := filepath.Join(runtimeRoot(t), "transport", "http")
	storeTypes := map[string]string{
		"RecordMutationExecutionStore":    "record",
		"ActionExecutionStore":            "action",
		"WorkflowWorkerStore":             "workflow",
		"IntegrationWorkerRepository":     "integration",
		"IdempotencyOperationsRepository": "deployment",
	}
	fileSet := token.NewFileSet()
	if err := filepath.WalkDir(transportRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		entrypoint, _ := filepath.Rel(transportRoot, path)
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				if owner, forbidden := storeTypes[value.Sel.Name]; forbidden {
					applicationService := strings.ToUpper(owner[:1]) + owner[1:] + "ApplicationService"
					t.Errorf("idempotency boundary violation: entry=%s owner=%s file=%s missing_contract=%s", entrypoint, owner, path, applicationService)
				}
			case *ast.Field:
				for _, name := range value.Names {
					field := strings.ToLower(name.Name)
					if (strings.Contains(field, "idempotency") || strings.Contains(field, "receipt")) && (strings.Contains(field, "store") || strings.Contains(field, "repository")) {
						t.Errorf("idempotency boundary violation: entry=%s owner=unknown file=%s missing_contract=owner ApplicationService field=%s", entrypoint, path, name.Name)
					}
				}
			}
			return true
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationHasNoPackageGlobalMapOrMutexFactSource(t *testing.T) {
	applicationRoot := filepath.Join(runtimeRoot(t), "application")
	fileSet := token.NewFileSet()
	if err := filepath.WalkDir(applicationRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(applicationRoot, path)
		owner := strings.Split(filepath.ToSlash(relative), "/")[0]
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			for _, rawSpec := range general.Specs {
				spec := rawSpec.(*ast.ValueSpec)
				for index, name := range spec.Names {
					forbidden := idempotencyGlobalFactType(spec.Type)
					if !forbidden && index < len(spec.Values) {
						forbidden = idempotencyGlobalFactType(spec.Values[index])
					}
					if forbidden {
						t.Errorf("idempotency boundary violation: entry=%s owner=%s file=%s missing_contract=%s Repository package_global=%s", relative, owner, path, owner, name.Name)
					}
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func idempotencyGlobalFactType(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.MapType:
		return true
	case *ast.CompositeLit:
		return idempotencyGlobalFactType(value.Type)
	case *ast.CallExpr:
		return len(value.Args) > 0 && idempotencyGlobalFactType(value.Args[0])
	case *ast.StarExpr:
		return idempotencyGlobalFactType(value.X)
	case *ast.SelectorExpr:
		qualifier, ok := value.X.(*ast.Ident)
		return ok && qualifier.Name == "sync" && (value.Sel.Name == "Mutex" || value.Sel.Name == "RWMutex")
	default:
		return false
	}
}

func TestWorkerCompletionAndHeartbeatContractsRequireLeaseOwnerAndFencingToken(t *testing.T) {
	root := runtimeRoot(t)
	methodContracts := []struct {
		owner, file, interfaceName string
		methods                    []string
	}{
		{owner: "action", file: "domain/action/contract/action_execution_claim.go", interfaceName: "ActionExecutionClaimStore", methods: []string{"HeartbeatExecution"}},
		{owner: "automation", file: "domain/automation/contract/automation_worker_contract.go", interfaceName: "AutomationWorkerStore", methods: []string{"HeartbeatInstruction", "CompleteInstruction"}},
		{owner: "integration", file: "domain/integration/repository/integration_repository.go", interfaceName: "IntegrationWorkerRepository", methods: []string{"HeartbeatEvent", "UpdateEventStatus", "ScheduleEventRetry", "HeartbeatOutbox", "UpdateOutboxStatus", "ScheduleOutboxRetry"}},
	}
	for _, contract := range methodContracts {
		path := filepath.Join(root, filepath.FromSlash(contract.file))
		methods := interfaceMethodParameterNames(t, path, contract.interfaceName)
		for _, method := range contract.methods {
			parameters := methods[method]
			if !parameters["expectedLeaseOwner"] || !parameters["expectedFencingToken"] {
				t.Errorf("idempotency worker boundary violation: entry=%s owner=%s file=%s missing_contract=expectedLeaseOwner+expectedFencingToken", method, contract.owner, path)
			}
		}
	}

	completionContracts := []struct {
		owner, file, typeName string
	}{
		{owner: "action", file: "domain/action/model/action_execution.go", typeName: "ActionExecutionCompletion"},
		{owner: "workflow", file: "domain/workflow/model/workflow_execution_receipt.go", typeName: "WorkflowExecutionReceiptCompletion"},
		{owner: "record", file: "domain/record/model/record_mutation_execution.go", typeName: "RecordMutationCompletion"},
		{owner: "changeplan", file: "domain/changeplan/model/changeplan_model.go", typeName: "ChangePlanOperationCompletion"},
	}
	for _, contract := range completionContracts {
		path := filepath.Join(root, filepath.FromSlash(contract.file))
		fields := structFieldNames(t, path, contract.typeName)
		if !fields["LeaseOwner"] || !fields["FencingToken"] {
			t.Errorf("idempotency worker boundary violation: entry=%s owner=%s file=%s missing_contract=LeaseOwner+FencingToken", contract.typeName, contract.owner, path)
		}
	}
}

func interfaceMethodParameterNames(t *testing.T, path, interfaceName string) map[string]map[string]bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]map[string]bool{}
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, rawSpec := range general.Specs {
			spec := rawSpec.(*ast.TypeSpec)
			contract, ok := spec.Type.(*ast.InterfaceType)
			if !ok || spec.Name.Name != interfaceName {
				continue
			}
			for _, method := range contract.Methods.List {
				if len(method.Names) == 0 {
					continue
				}
				function := method.Type.(*ast.FuncType)
				names := map[string]bool{}
				for _, parameter := range function.Params.List {
					for _, name := range parameter.Names {
						names[name.Name] = true
					}
				}
				result[method.Names[0].Name] = names
			}
		}
	}
	return result
}

func structFieldNames(t *testing.T, path, typeName string) map[string]bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, declaration := range parsed.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, rawSpec := range general.Specs {
			spec := rawSpec.(*ast.TypeSpec)
			structure, ok := spec.Type.(*ast.StructType)
			if !ok || spec.Name.Name != typeName {
				continue
			}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					fields[name.Name] = true
				}
			}
		}
	}
	return fields
}
