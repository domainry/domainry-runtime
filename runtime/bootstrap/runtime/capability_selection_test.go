package runtime

import (
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func TestProjectSchemaCapabilitiesFollowProjectOwnedReferences(t *testing.T) {
	minimal := projectSchemaCapabilities(projectmodel.RuntimeModel{}, runtimeext.ProjectDefinitions{}, nil)
	if minimal.Workflow || minimal.Automation || minimal.Uploads || minimal.Lifecycle || minimal.ReleaseCoordination {
		t.Fatalf("minimal project selected native capabilities: %#v", minimal)
	}

	selected := projectSchemaCapabilities(projectmodel.RuntimeModel{Objects: []definitionmodel.ObjectSchema{{
		Key: "documents", Fields: []definitionmodel.FieldSchema{{Key: "attachment", Type: "file"}},
	}}}, runtimeext.ProjectDefinitions{
		Workflows:       []definitionmodel.WorkflowSchema{{Key: "review"}},
		AutomationRules: []automationmodel.AutomationRuleSchema{{Key: "route"}},
	}, nil)
	if !selected.Workflow || !selected.Automation || !selected.Uploads || !selected.Lifecycle {
		t.Fatalf("project references did not select native capabilities: %#v", selected)
	}
}

func TestProjectSchemaCapabilitiesSelectUploadsForHandlerAndAgentUse(t *testing.T) {
	fromHandler := projectSchemaCapabilities(projectmodel.RuntimeModel{}, runtimeext.ProjectDefinitions{}, []runtimeext.HandlerDescriptor{{FileCapabilities: []string{runtimeext.FileOperationVerifyClean}}})
	if !fromHandler.Uploads {
		t.Fatal("handler file capability did not select Uploads")
	}
	fromAgent := projectSchemaCapabilities(projectmodel.RuntimeModel{}, runtimeext.ProjectDefinitions{Agents: []agentsdk.AgentSchema{{Key: "assistant"}}}, nil)
	if !fromAgent.Uploads {
		t.Fatal("Agent capability did not select attachment uploads")
	}
}

func TestProjectSchemaCapabilitiesSelectLifecycleForAccountErasure(t *testing.T) {
	selected := projectSchemaCapabilities(projectmodel.RuntimeModel{}, runtimeext.ProjectDefinitions{}, []runtimeext.HandlerDescriptor{{
		AccountErasure: &runtimeext.AccountErasureCapability{},
	}})
	if !selected.Lifecycle || selected.Uploads {
		t.Fatalf("account erasure capability selection=%#v", selected)
	}
}

func TestRuntimeAuthorizationRegistryOmitsUnselectedNativeCapabilities(t *testing.T) {
	minimal := persistence.RuntimeSchemaCapabilities{}
	contracts := runtimeEndpointContractsForCapabilities(minimal)
	for identity, contract := range contracts {
		switch contract.SourceOwner {
		case "workflows", "automation", "uploads":
			t.Fatalf("unselected endpoint %q from owner %q remains published", identity, contract.SourceOwner)
		}
	}

	registry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "domainry-runtime", nil, minimal)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"runtime.workflows.list_workflow_processes",
		"runtime.automation.list_automation_rules",
		"runtime.automation.enable_rule",
		"runtime.uploads.upload_file",
		"runtime.public_resources.files.read",
	} {
		if _, found := registry.Definition(key); found {
			t.Fatalf("unselected Action %q remains published", key)
		}
	}
	if _, found := registry.Definition("runtime.public_resources.read"); !found {
		t.Fatal("core public-resource read Action was removed")
	}

	fullRegistry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "domainry-runtime", nil, persistence.FullRuntimeSchemaCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"runtime.workflows.list_workflow_processes", "runtime.automation.list_automation_rules", "runtime.uploads.upload_file", "runtime.public_resources.files.read"} {
		if _, found := fullRegistry.Definition(key); !found {
			t.Fatalf("selected Action %q is missing", key)
		}
	}
}

func TestUnselectedCapabilitiesAllowEmptyDefinitions(t *testing.T) {
	if err := validateSelectedCapabilityFactories(runtimeext.ProjectDefinitions{}, nil, nil, nil, nil, false, nil, nil); err != nil {
		t.Fatalf("empty optional capability set: %v", err)
	}
}

func TestUnselectedSchedulerRejectsScheduleDefinitions(t *testing.T) {
	err := validateSelectedCapabilityFactories(runtimeext.ProjectDefinitions{
		Schedules: []schedulersdk.Definition{{Key: "daily"}},
	}, nil, nil, nil, nil, false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Scheduler SDK Factory") {
		t.Fatalf("missing Scheduler selection error=%v", err)
	}
}

func TestUnselectedNotificationRejectsNotificationDefinitions(t *testing.T) {
	err := validateSelectedCapabilityFactories(runtimeext.ProjectDefinitions{
		NotificationTemplates: []notificationmodel.NotificationTemplate{{}},
	}, nil, nil, nil, nil, false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Notification SDK Factory") {
		t.Fatalf("missing Notification selection error=%v", err)
	}
}

func TestUnselectedReportRejectsDefinitionsAndScheduleTargets(t *testing.T) {
	definitions := runtimeext.ProjectDefinitions{Reports: []runtimeext.ReportDefinition{{}}}
	if err := validateSelectedCapabilityFactories(definitions, nil, nil, nil, nil, false, nil, nil); err == nil || !strings.Contains(err.Error(), "Report SDK Factory") {
		t.Fatalf("missing Report definition selection error=%v", err)
	}
	definitions = runtimeext.ProjectDefinitions{Schedules: []schedulersdk.Definition{{Target: schedulersdk.TargetRef{Owner: "report_snapshot_refresh"}}}}
	if err := validateSelectedCapabilityFactories(definitions, nil, nil, nil, nil, false, nil, nil); err == nil || !strings.Contains(err.Error(), "Report SDK Factory") {
		t.Fatalf("missing Report target selection error=%v", err)
	}
}

func TestUnselectedAgentRejectsAgentDefinitions(t *testing.T) {
	err := validateSelectedCapabilityFactories(runtimeext.ProjectDefinitions{
		Agents: []agentsdk.AgentSchema{{Key: "reviewer"}},
	}, nil, nil, nil, nil, false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Agent SDK Factory") {
		t.Fatalf("missing Agent selection error=%v", err)
	}
}

func TestUnselectedIntegrationRejectsMappingsAndConnectorProviders(t *testing.T) {
	definitions := runtimeext.ProjectDefinitions{IntegrationMappings: []integrationsdk.EventMappingRequirement{{}}}
	if err := validateSelectedCapabilityFactories(definitions, nil, nil, nil, nil, false, nil, nil); err == nil || !strings.Contains(err.Error(), "Integration SDK Factory") {
		t.Fatalf("missing Integration mapping selection error=%v", err)
	}
	if err := validateSelectedCapabilityFactories(runtimeext.ProjectDefinitions{}, nil, nil, nil, nil, true, nil, nil); err == nil || !strings.Contains(err.Error(), "Integration SDK Factory") {
		t.Fatalf("missing Connector provider selection error=%v", err)
	}
}

func TestUnselectedCapabilitiesRejectWorkflowAndHandlerReferences(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{
		{Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "notify"}}},
		{Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "review"}}},
	}}}
	definitions := runtimeext.ProjectDefinitions{Workflows: []definitionmodel.WorkflowSchema{workflow}}
	if err := validateSelectedCapabilityFactories(definitions, nil, nil, nil, nil, false, nil, nil); err == nil || !strings.Contains(err.Error(), "Notification SDK Factory") {
		t.Fatalf("missing workflow Notification selection error=%v", err)
	}
	definitions.Workflows[0].Graph.Nodes = definitions.Workflows[0].Graph.Nodes[1:]
	if err := validateSelectedCapabilityFactories(definitions, nil, nil, nil, nil, false, nil, nil); err == nil || !strings.Contains(err.Error(), "Agent SDK Factory") {
		t.Fatalf("missing workflow Agent selection error=%v", err)
	}
	if err := validateSelectedCapabilityFactories(runtimeext.ProjectDefinitions{}, []runtimeext.HandlerDescriptor{{NotificationEventTypes: []string{"order.completed"}}}, nil, nil, nil, false, nil, nil); err == nil || !strings.Contains(err.Error(), "Notification SDK Factory") {
		t.Fatalf("missing handler Notification selection error=%v", err)
	}
}
