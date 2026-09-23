package automation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type automationRuleRegistryStub struct {
	rules map[string]automationmodel.AutomationRuleSchema
}

func (r *automationRuleRegistryStub) List() []automationmodel.AutomationRuleSchema {
	out := make([]automationmodel.AutomationRuleSchema, 0, len(r.rules))
	for _, rule := range r.rules {
		out = append(out, rule)
	}
	return out
}

func (r *automationRuleRegistryStub) Get(key string) (automationmodel.AutomationRuleSchema, bool) {
	rule, found := r.rules[strings.TrimSpace(key)]
	return rule, found
}

func (r *automationRuleRegistryStub) Set(key string, rule automationmodel.AutomationRuleSchema) {
	r.rules[strings.TrimSpace(key)] = rule
}

type automationExecutionRepositoryStub struct {
	automationrepository.AutomationExecutionRepository
	items  []automationmodel.AutomationRuleExecution
	err    error
	filter automationmodel.AutomationExecutionFilter
}

func (r *automationExecutionRepositoryStub) ListExecutions(_ context.Context, _ string, filter automationmodel.AutomationExecutionFilter) ([]automationmodel.AutomationRuleExecution, error) {
	r.filter = filter
	return append([]automationmodel.AutomationRuleExecution(nil), r.items...), r.err
}

func (r *automationExecutionRepositoryStub) InsertExecution(_ context.Context, _ string, execution automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error) {
	r.items = append(r.items, execution)
	return execution, nil
}

type automationMetadataStub struct{ err error }

type automationHandlerCapture struct {
	serviceErr error
}

type automationHandlerFixture struct {
	handler     *AutomationHandler
	registry    *automationRuleRegistryStub
	executions  *automationExecutionRepositoryStub
	metadata    *automationMetadataStub
	capture     *automationHandlerCapture
	principal   *principalmodel.Principal
	validateErr error
}

var automationHTTPPermissions = []string{
	"runtime.automation.get_execution_catalog",
	"runtime.automation.list_automation_rules",
	"runtime.automation.get_automation_rule",
	"runtime.automation.list_automation_executions",
	"runtime.automation.simulate_rule",
}

func automationTestRule(key string) automationmodel.AutomationRuleSchema {
	return automationmodel.AutomationRuleSchema{Key: key, Name: "Rule " + key, ObjectKey: "customer", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"}, Instructions: []automationmodel.AutomationInstructionSchema{}}
}

func newAutomationHandlerFixture() *automationHandlerFixture {
	registry := &automationRuleRegistryStub{rules: map[string]automationmodel.AutomationRuleSchema{"welcome": automationTestRule("welcome")}}
	executions := &automationExecutionRepositoryStub{items: []automationmodel.AutomationRuleExecution{{ID: "execution-1", RuleKey: "welcome", Status: "succeeded"}}}
	metadata := &automationMetadataStub{}
	principal := accessfixture.AttachPointer(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Key: "automation-manager", Permissions: append([]string(nil), automationHTTPPermissions...)})
	capture := &automationHandlerCapture{}
	fixture := &automationHandlerFixture{registry: registry, executions: executions, metadata: metadata, capture: capture, principal: principal}
	service := automationapplication.NewAutomationApplicationService(automationapplication.AutomationApplicationDependencies{
		Rules: registry, ExecutionRepository: executions,
		Schema: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
			return appschemamodel.ApplicationSchemaSnapshot{}
		},
		ValidateRule: func(context.Context, automationmodel.AutomationRuleSchema) error { return fixture.validateErr },
	})
	fixture.handler = NewAutomationHandler(AutomationDependencies{
		Commands:  service,
		Principal: func(*http.Request) principalmodel.Principal { return *principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			capture.serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, value any) bool {
			if err := json.NewDecoder(r.Body).Decode(value); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
	})
	return fixture
}

func automationRequest(method, target, body, ruleKey string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.SetPathValue("ruleKey", ruleKey)
	return request
}

func TestAutomationRoutesBindMethodsAndPaths(t *testing.T) {
	fixture := newAutomationHandlerFixture()
	mux := http.NewServeMux()
	fixture.handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/automation/rules", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"welcome"`) {
		t.Fatalf("list route status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/automation/rules/welcome", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method route status=%d", response.Code)
	}
}

func TestAutomationHandlersListCapabilitiesHistoryAndGet(t *testing.T) {
	fixture := newAutomationHandlerFixture()
	rules := httptest.NewRecorder()
	fixture.handler.listAutomationRules(rules, automationRequest(http.MethodGet, "/automation/rules", "", ""))
	if rules.Code != http.StatusOK || !strings.Contains(rules.Body.String(), `"count":1`) || !strings.Contains(rules.Body.String(), `"welcome"`) {
		t.Fatalf("rules status=%d body=%s", rules.Code, rules.Body.String())
	}

	catalog := httptest.NewRecorder()
	fixture.handler.automationExecutionCatalog(catalog, automationRequest(http.MethodGet, "/automation/execution-catalog", "", ""))
	if catalog.Code != http.StatusOK {
		t.Fatalf("execution catalog status=%d body=%s", catalog.Code, catalog.Body.String())
	}

	history := httptest.NewRecorder()
	fixture.handler.listAutomationExecutions(history, automationRequest(http.MethodGet, "/automation/executions?rule_key=welcome&object_key=customer&record_id=record-1&phase=before&status=succeeded&connector_key=crm&from=2026-01-01&to=2026-02-01&limit=25", "", ""))
	filter := fixture.executions.filter
	if history.Code != http.StatusOK || filter.RuleKey != "welcome" || filter.ObjectKey != "customer" || filter.RecordID != "record-1" || filter.Phase != "before" || filter.Status != "succeeded" || filter.ConnectorKey != "crm" || filter.From != "2026-01-01" || filter.To != "2026-02-01" || filter.Limit != 25 {
		t.Fatalf("history status=%d filter=%#v body=%s", history.Code, filter, history.Body.String())
	}

	get := httptest.NewRecorder()
	fixture.handler.getAutomationRule(get, automationRequest(http.MethodGet, "/", "", " welcome "))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"key":"welcome"`) {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}
	fixture.capture.serviceErr = nil
	missing := httptest.NewRecorder()
	fixture.handler.getAutomationRule(missing, automationRequest(http.MethodGet, "/", "", "missing"))
	if missing.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("missing status=%d error=%v", missing.Code, fixture.capture.serviceErr)
	}

}

func TestAutomationHandlersMapHistoryRulesAndCapabilityErrors(t *testing.T) {
	fixture := newAutomationHandlerFixture()
	accessfixture.Set(fixture.principal, accessfixture.Bundle{})
	for name, handle := range map[string]func(http.ResponseWriter, *http.Request){
		"rules":             fixture.handler.listAutomationRules,
		"execution catalog": fixture.handler.automationExecutionCatalog,
		"history":           fixture.handler.listAutomationExecutions,
	} {
		t.Run(name, func(t *testing.T) {
			fixture.capture.serviceErr = nil
			response := httptest.NewRecorder()
			handle(response, automationRequest(http.MethodGet, "/", "", ""))
			if response.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
				t.Fatalf("status=%d error=%v", response.Code, fixture.capture.serviceErr)
			}
		})
	}
	accessfixture.Set(fixture.principal, accessfixture.Bundle{Permissions: append([]string(nil), automationHTTPPermissions...)})
	fixture.executions.err = errors.New("history failed")
	fixture.capture.serviceErr = nil
	response := httptest.NewRecorder()
	fixture.handler.listAutomationExecutions(response, automationRequest(http.MethodGet, "/?limit=invalid", "", ""))
	if response.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil || fixture.executions.filter.Limit != 0 {
		t.Fatalf("history failure status=%d error=%v filter=%#v", response.Code, fixture.capture.serviceErr, fixture.executions.filter)
	}
}

func TestAutomationSimulationHandlerSuccessAndServiceFailure(t *testing.T) {
	fixture := newAutomationHandlerFixture()
	badJSON := httptest.NewRecorder()
	fixture.handler.simulateAutomationRule(badJSON, automationRequest(http.MethodPost, "/", "{", "welcome"))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status=%d", badJSON.Code)
	}

	success := httptest.NewRecorder()
	fixture.handler.simulateAutomationRule(success, automationRequest(http.MethodPost, "/", `{"input":{"name":"Test"}}`, " welcome "))
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"rule_key":"welcome"`) || !strings.Contains(success.Body.String(), `"tested_node_id":"save"`) {
		t.Fatalf("success status=%d body=%s", success.Code, success.Body.String())
	}

	fixture.capture.serviceErr = nil
	missing := httptest.NewRecorder()
	fixture.handler.simulateAutomationRule(missing, automationRequest(http.MethodPost, "/", `{}`, "missing"))
	if missing.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("missing status=%d error=%v", missing.Code, fixture.capture.serviceErr)
	}
}
