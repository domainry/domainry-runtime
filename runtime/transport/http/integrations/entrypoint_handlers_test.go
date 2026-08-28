package integrations_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type integrationHTTPWorkflowApplication struct{}

func (integrationHTTPWorkflowApplication) RunIntegrationWorkflow(_ context.Context, workflowKey string, payload map[string]any, _ principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return workflowmodel.WorkflowRunResult{WorkflowKey: workflowKey, Name: "Probe workflow", Status: "succeeded", Payload: payload}, nil
}

func TestIntegrationEntrypointHTTP(t *testing.T) {
	store, application := newIntegrationEntrypointHTTPApplication(t)
	defer store.Close()
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}, RequestID: "entrypoint-1"}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}})
	call := integrationManagementHTTPCall(application, &principal)

	if response := call(http.MethodPut, "/tenant-admin/integrations/external-identities/probe-user", `{"provider":"probe","external_subject":"user-1","actor_id":"service-user","role_key":"integration-client"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("identity status=%d body=%s", response.Code, response.Body.String())
	}
	externalIdentity := `"external_identity":{"provider":"probe","external_subject":"user-1"}`

	for _, request := range []struct{ path, body string }{
		{"/integrations/entrypoints/workflows/probe/run", `{`},
		{"/integrations/entrypoints/objects/customer/records/customer-1/actions/refresh", `{`},
		{"/integrations/agents/assistant/tools/readRecord/invoke", `{`},
	} {
		if response := call(http.MethodPost, request.path, request.body, nil); response.Code != http.StatusBadRequest {
			t.Fatalf("%s invalid JSON status=%d body=%s", request.path, response.Code, response.Body.String())
		}
	}
	for _, request := range []struct{ path, body, want string }{
		{"/integrations/entrypoints/workflows/probe/run", `{` + externalIdentity + `,"payload":{"value":1}}`, `"workflow_key":"probe"`},
		{"/integrations/entrypoints/objects/customer/records/customer-1/actions/refresh", `{` + externalIdentity + `,"data":{"value":1}}`, `"action_key":"refresh"`},
		{"/integrations/agents/assistant/tools/readRecord/invoke", `{` + externalIdentity + `,"input":{"object_key":"customer","record_id":"customer-1"},"request_ref":"agent-1"}`, `"tool":"readRecord"`},
	} {
		response := call(http.MethodPost, request.path, request.body, nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), request.want) {
			t.Fatalf("%s status=%d body=%s", request.path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{
		"/integrations/entrypoints/workflows/probe/run",
		"/integrations/entrypoints/objects/customer/records/customer-1/actions/refresh",
		"/integrations/agents/assistant/tools/readRecord/invoke",
	} {
		if response := call(http.MethodPost, path, `{}`, nil); response.Code != http.StatusBadRequest {
			t.Fatalf("%s missing identity status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	accessfixture.Set(&principal, accessfixture.Bundle{})
	if response := call(http.MethodPost, "/integrations/agents/assistant/tools/readRecord/invoke", `{`+externalIdentity+`}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("agent permission status=%d body=%s", response.Code, response.Body.String())
	}
}

func newIntegrationEntrypointHTTPApplication(t *testing.T) (*database.RuntimeStore, *integrationapplication.IntegrationApplicationService) {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "integration-entrypoint.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	application := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{
		ConfigRepository:   integrationpersistence.NewIntegrationConfigStore(store),
		EventRepository:    integrationpersistence.NewIntegrationEventStore(store),
		DeliveryRepository: integrationpersistence.NewIntegrationDeliveryStore(store),
		WorkerRepository:   integrationpersistence.NewIntegrationWorkerStore(store),
		Registry:           integrationapplication.NewConnectorRegistry(integrationmodel.IntegrationSchema{}),
		PrincipalResolver: func(_ context.Context, actorID, roleKey, _ string) principalmodel.Principal {
			if actorID == "service-user" && roleKey == "integration-client" {
				return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: actorID}}, accessfixture.Bundle{Key: roleKey, Permissions: []string{"customer.read"}})
			}
			return principalmodel.Principal{}
		},
		Schema: func(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
			return metadatamodel.MetadataSchemaSnapshot{Agents: []agentmodel.AgentSchema{{Key: "assistant", Name: "Assistant", Tools: []string{"readRecord"}}}}
		},
		InvokeAction: func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			return actionmodel.ActionInvocationResult{Record: &actionmodel.ActionResult{ActionKey: invocation.ActionKey, ObjectKey: invocation.ObjectKey, RecordID: invocation.RecordID, Message: "ok"}}, nil
		},
		Workflows: integrationHTTPWorkflowApplication{},
	})
	return store, application
}
