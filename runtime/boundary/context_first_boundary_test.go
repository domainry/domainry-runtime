package boundary_test

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeRepositoryContractsAreContextFirst(t *testing.T) {
	allowed := stringSet("IntegrationCredentialLeaseRepositoryProvider.CredentialLeaseRepository")
	seen := map[string]bool{}
	root := runtimeRoot(t)
	walkProductionGo(t, root, func(path string, file *ast.File) {
		contextAliases := runtimeContextAliases(file)
		if !strings.Contains(filepath.ToSlash(path), "/domain/") || !strings.Contains(filepath.ToSlash(path), "/repository/") {
			return
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				contract, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				for _, method := range contract.Methods.List {
					function, ok := method.Type.(*ast.FuncType)
					if !ok || len(method.Names) == 0 {
						continue
					}
					symbol := typeSpec.Name.Name + "." + method.Names[0].Name
					if allowed[symbol] {
						seen[symbol] = true
						continue
					}
					if function.Params == nil || len(function.Params.List) == 0 || !isRuntimeContextType(function.Params.List[0].Type, contextAliases) {
						t.Errorf("Repository I/O contract must receive context.Context first: %s %s.%s", path, typeSpec.Name.Name, method.Names[0].Name)
					}
				}
			}
		}
	})
	for symbol := range allowed {
		if !seen[symbol] {
			t.Errorf("Context-free Repository provider exception is stale; remove or review renamed symbol: %s", symbol)
		}
	}
}

func TestRuntimeContextParameterIsAlwaysFirst(t *testing.T) {
	root := runtimeRoot(t)
	walkProductionGo(t, root, func(path string, file *ast.File) {
		aliases := runtimeContextAliases(file)
		ast.Inspect(file, func(node ast.Node) bool {
			var params *ast.FieldList
			var symbol string
			switch value := node.(type) {
			case *ast.FuncDecl:
				params, symbol = value.Type.Params, value.Name.Name
			case *ast.FuncType:
				params, symbol = value.Params, "function type"
			default:
				return true
			}
			if params == nil {
				return true
			}
			for index, parameter := range params.List {
				if isRuntimeContextType(parameter.Type, aliases) && index != 0 {
					t.Errorf("context.Context must be the first explicit parameter: %s declares %s", path, symbol)
				}
			}
			return true
		})
	})
}

func TestRuntimeBoundaryInterfacesAreContextFirstOrReviewedPure(t *testing.T) {
	allowedDomainContractMethods := stringSet(
		"AutomationRuleRegistry.List", "AutomationRuleRegistry.Get",
		"SnapshotSource.ChangePlanSnapshot", "ReferenceGraphSource.ChangePlanReferenceGraph",
		"ConfigValidator.ValidateConfig", "SchemaProvider.ProviderSchema",
		"IntegrationAutomationApplication.ValidateIntegrationOutput",
		"OperationIdentityProvider.OperationIdentity",
		"NotificationInboxActionRegistry.ResolveNotificationInboxAction",
		"NotificationEventTypeRegistry.ResolveNotificationEventType",
		"NotificationRuleRegistry.ResolveNotificationRule",
	)
	seenAllowed := map[string]bool{}
	root := runtimeRoot(t)
	walkProductionGo(t, root, func(path string, file *ast.File) {
		slashPath := filepath.ToSlash(path)
		aliases := runtimeContextAliases(file)
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				contract, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				requireContext := strings.Contains(slashPath, "/domain/") && strings.Contains(slashPath, "/contract/")
				requireContext = requireContext || strings.Contains(slashPath, "/application/") && runtimeCrossBoundaryInterface(typeSpec.Name.Name)
				if !requireContext {
					continue
				}
				for _, method := range contract.Methods.List {
					function, ok := method.Type.(*ast.FuncType)
					if !ok || len(method.Names) == 0 {
						continue
					}
					symbol := typeSpec.Name.Name + "." + method.Names[0].Name
					if allowedDomainContractMethods[symbol] {
						seenAllowed[symbol] = true
						continue
					}
					if function.Params == nil || len(function.Params.List) == 0 || !isRuntimeContextType(function.Params.List[0].Type, aliases) {
						t.Errorf("cross-boundary interface method must receive context.Context first: %s declares %s", path, symbol)
					}
				}
			}
		}
	})
	for symbol := range allowedDomainContractMethods {
		if !seenAllowed[symbol] {
			t.Errorf("reviewed pure Domain contract exception is stale: %s", symbol)
		}
	}
}

func runtimeCrossBoundaryInterface(name string) bool {
	for _, suffix := range []string{"Application", "ApplicationPort", "Source", "Reader", "Writer", "Repository"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func TestRuntimeApplicationContextFreeMethodsMatchReviewedPureOrConstructionExceptions(t *testing.T) {
	allowed := stringSet(
		"ActionApplicationService.CatalogValidationErrors",
		"ActionApplicationService.Definitions",
		"ActionApplicationService.ReplaceDefinitions",
		"AuditApplicationService.ConfigureBusinessExport", "AuditApplicationService.SetBusinessExportClock",
		"AuditApplicationService.SetEventProjector",
		"AutomationApplicationService.AfterOutbox", "AutomationApplicationService.ValidateIntegrationOutput",
		"BusinessSystemApplicationService.RuntimeProjectionConfigured", "BusinessSystemApplicationService.SetEvidenceRepository",
		"BusinessSeedAuthoringApplicationService.AuthorizeSeedRecordUpsert",
		"CapabilityAuthoringApplicationService.UsePreferenceReferenceSource", "CapabilityAuthoringApplicationService.UseRuleSetReferenceSource",
		"DeploymentFrontendCapabilityApplicationService.Configured", "DeploymentFrontendCapabilityApplicationService.SnapshotManifest", "DeploymentFrontendCapabilityApplicationService.ValidateManifestDefinition",
		"DeploymentRuntimeReleaseCohortApplicationService.HeartbeatInterval",
		"IntegrationApplicationService.AdapterForConnection",
		"IntegrationApplicationService.AuthorizeIntegrationConnectionUpsert",
		"IntegrationApplicationService.ConnectorAdapterReady", "IntegrationApplicationService.ConnectorDefinition", "IntegrationApplicationService.ConnectorExists", "IntegrationApplicationService.EventMappingForEvent",
		"IntegrationApplicationService.IntegrationEventHandler", "IntegrationApplicationService.IntegrationOperation", "IntegrationApplicationService.IntegrationOutboxSender", "IntegrationApplicationService.PlanEventMappingExecution",
		"IntegrationApplicationService.ProviderSupportsIntegrationOperation", "IntegrationApplicationService.RegisterBuiltinConnectorDefinitions",
		"IntegrationApplicationService.RegisterDefaultIntegrationOutboxSenders", "IntegrationApplicationService.RegisterIntegrationEventHandler",
		"IntegrationApplicationService.RegisterIntegrationOutboxSender", "IntegrationApplicationService.RegisterProviderIntegrationOutboxSenders",
		"IntegrationApplicationService.RegisterSharedIntegrationOutboxSenders",
		"IntegrationApplicationService.ValidateAdapterConfig", "IntegrationApplicationService.ValidateAutomationOperationOutput",
		"MetadataApplicationService.AddReloadObserver", "MetadataApplicationService.UseActionDefinitionSource",
		"OperationsApplicationService.Definitions", "OperationsApplicationService.RegisterBreakGlass", "OperationsApplicationService.RegisterDeadLetterOwner", "OperationsApplicationService.RegisterDiagnostics",
		"OperationsApplicationService.UseDirectAuthoringProjection",
		"PipelineApplicationService.ApplyStageSLA", "PipelineApplicationService.ValidateStagePermission", "PipelineTransitionApplicationService.IsAction",
		"RecordApplicationService.CanAccessRecord", "RecordApplicationService.NormalizeListQuery", "RecordApplicationService.ObjectForAction",
		"RecordApplicationService.RegisterOwnedBatchProcessor", "RecordBatchJobApplicationService.RegisterOwnedProcessor",
		"SchedulerApplicationService.ConfigureWorker", "SchedulerApplicationService.UseDefinitionHistoryReader", "SchedulerApplicationService.UseDefinitionSource", "SchedulerApplicationService.UseNotificationCompiler", "SchedulerApplicationService.UseReportSnapshotRuntime", "SchedulerApplicationService.WorkerConfig",
	)
	seen := map[string]bool{}
	root := filepath.Join(runtimeRoot(t), "application")
	walkProductionGo(t, root, func(path string, file *ast.File) {
		contextAliases := runtimeContextAliases(file)
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || !function.Name.IsExported() {
				continue
			}
			receiver := runtimeReceiverName(function.Recv.List[0].Type)
			if !strings.HasSuffix(receiver, "ApplicationService") {
				continue
			}
			if function.Type.Params != nil && len(function.Type.Params.List) > 0 && isRuntimeContextType(function.Type.Params.List[0].Type, contextAliases) {
				continue
			}
			symbol := receiver + "." + function.Name.Name
			seen[symbol] = true
			if !allowed[symbol] {
				t.Errorf("Application method without context must be reviewed as pure/construction-only: %s declares %s", path, symbol)
			}
		}
	})
	for symbol := range allowed {
		if !seen[symbol] {
			t.Errorf("Context-free Application exception is stale; remove or review renamed symbol: %s", symbol)
		}
	}
}

func TestRuntimeContextCancellationEvidenceIsRetained(t *testing.T) {
	root := runtimeRoot(t)
	evidence := map[string][]string{
		"domain/record/service/record_domain_service_context_test.go":                          {"context.WithTimeout", "context.DeadlineExceeded"},
		"application/scheduler/scheduler_context_test.go":                                      {"context.WithCancel", "context.Canceled"},
		"application/integration/integration_application_service_connection_lifecycle_test.go": {"context.WithCancel", "context.Canceled"},
		"infrastructure/persistence/database/integration/worker_store_test.go":                 {"context.WithCancel", "context.Canceled"},
	}
	for relative, markers := range evidence {
		path := filepath.Join(root, filepath.FromSlash(relative))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("required Context cancellation evidence is missing: %s: %v", path, err)
			continue
		}
		for _, marker := range markers {
			if !strings.Contains(string(content), marker) {
				t.Errorf("required Context cancellation evidence %s lacks %q", path, marker)
			}
		}
	}
}

func TestRuntimeDoesNotStoreOrCutOffRequestContext(t *testing.T) {
	root := runtimeRoot(t)
	hostPath := filepath.Clean(filepath.Join(root, "..", "pkg", "runtimehost", "host.go"))
	check := func(path string, file *ast.File) {
		contextAliases := runtimeContextAliases(file)
		ast.Inspect(file, func(node ast.Node) bool {
			structure, ok := node.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range structure.Fields.List {
				if isRuntimeContextType(field.Type, contextAliases) {
					t.Errorf("context.Context must not be stored in a struct: %s", path)
				}
			}
			return true
		})
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		if strings.Contains(text, "context.TODO(") {
			t.Errorf("production request chain must not use context.TODO: %s", path)
		}
		if strings.Contains(text, "context.Background(") && filepath.Clean(path) != hostPath {
			t.Errorf("production request chain must not replace caller context with context.Background: %s", path)
		}
	}
	walkProductionGo(t, root, check)
	walkProductionGo(t, filepath.Join(root, "..", "pkg", "runtimehost"), check)
}

func runtimeContextAliases(file *ast.File) map[string]bool {
	aliases := technicalLayoutStringSet("context")
	for _, specification := range file.Imports {
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil || path != "context" || specification.Name == nil {
			continue
		}
		if name := specification.Name.Name; name != "_" && name != "." {
			aliases[name] = true
		}
	}
	return aliases
}

func runtimeReceiverName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return runtimeReceiverName(value.X)
	case *ast.IndexExpr:
		return runtimeReceiverName(value.X)
	case *ast.IndexListExpr:
		return runtimeReceiverName(value.X)
	default:
		return ""
	}
}
