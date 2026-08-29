package agentdialog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type agentAnalysisRecordRepository struct {
	recordrepository.RecordRepository
	page  recordmodel.RecordPageResult
	err   error
	query recordmodel.RecordListQuery
}

func (repository *agentAnalysisRecordRepository) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	repository.query = query
	return repository.page, repository.err
}

type agentAnalysisSchemaProvider struct {
	snapshot metadatamodel.ApplicationSchemaSnapshot
}

func (provider agentAnalysisSchemaProvider) SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot {
	return provider.snapshot
}

type agentAnalysisHTTPResult struct {
	code          string
	serviceErrors int
	audits        int
}

func agentAnalysisHandler(repository *agentDialogStateRepository, principal principalmodel.Principal) (*AgentDialogHandler, *agentAnalysisHTTPResult) {
	snapshot := metadatamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "secret", Type: "text"}}}},
		Reports: []reportmodel.ReportSchema{{Key: "customer.summary"}},
	}
	catalog := metadataapplication.NewMetadataSchemaApplicationService(agentAnalysisSchemaProvider{snapshot: snapshot}, nil)
	result := &agentAnalysisHTTPResult{}
	return NewAgentDialogHandler(AgentDialogDependencies{
		AnalysisCatalog: catalog, ReportGovernance: agentapplication.NewAgentApplicationService(repository),
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			result.code = code
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) {
			result.serviceErrors++
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
		DecodeJSON: func(w http.ResponseWriter, request *http.Request, target any) bool {
			if err := json.NewDecoder(request.Body).Decode(target); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
		SecurityAuditForPrincipal: func(*http.Request, principalmodel.Principal, string, string, map[string]any) { result.audits++ },
	}), result
}

func agentAnalysisPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "analyst"}}, accessfixture.Bundle{
		Key: "analyst", Permissions: []string{"customer.read"}, RecordScope: "all_records",
		DataPolicies:  []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}},
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "secret", Read: true, Masked: true}},
	},
	)
}

func agentAnalysisRecordApplication(repository *agentAnalysisRecordRepository) *recordapplication.RecordApplicationService {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "status", Type: "text"}}}
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} }})
	reader := recordservice.NewRecordReadDomainService(recordservice.RecordReadDependencies{Repository: repository, Policy: policy})
	domain := recordservice.NewRecordDomainService(recordservice.RecordDomainServiceDependencies{Repository: repository, Reader: reader})
	return &recordapplication.RecordApplicationService{RecordDomainService: domain}
}

func TestAgentDialogAnalysisQueryRejectsInvalidInputAndInvisibleResources(t *testing.T) {
	handler, result := agentAnalysisHandler(newAgentDialogStateRepository(), agentAnalysisPrincipal())
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "invalid JSON", body: "{", status: http.StatusBadRequest},
		{name: "missing intent", body: `{}`, status: http.StatusBadRequest, code: "agent_analysis.intent_required"},
		{name: "write SQL", body: `{"intent":"change","sql":"WITH changed AS (DELETE FROM customer RETURNING *) SELECT * FROM changed"}`, status: http.StatusBadRequest, code: "agent_analysis.sql_readonly_required"},
		{name: "multiple SQL", body: `{"intent":"query","sql":"SELECT 1; SELECT 2"}`, status: http.StatusBadRequest, code: "agent_analysis.sql_multi_statement_denied"},
		{name: "object denied", body: `{"intent":"query","metric_spec":{"object_key":"order"}}`, status: http.StatusForbidden, code: "agent_analysis.object_denied"},
		{name: "report denied", body: `{"intent":"query","metric_spec":{"object_key":"customer","report_key":"missing"}}`, status: http.StatusForbidden, code: "agent_analysis.report_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result.code = ""
			response := httptest.NewRecorder()
			handler.agentDialogAnalysisQuery(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/analysis/query", strings.NewReader(test.body)))
			if response.Code != test.status || result.code != test.code {
				t.Fatalf("status=%d code=%q want status=%d code=%q body=%s", response.Code, result.code, test.status, test.code, response.Body.String())
			}
		})
	}
}

func TestAgentDialogAnalysisQueryValidatesDryRunSQLAndSchemaOnlyRequests(t *testing.T) {
	handler, result := agentAnalysisHandler(newAgentDialogStateRepository(), agentAnalysisPrincipal())
	tests := []struct {
		name string
		body string
	}{
		{name: "dry run", body: `{"intent":"Review customers","dry_run":true,"max_rows":-1,"metric_spec":{"object_key":"customer","report_key":"customer.summary","follow_up":{"title":"Create task"}}}`},
		{name: "SQL validation", body: `{"intent":"Explain customers","sql":"SELECT * FROM customer","max_rows":101,"metric_spec":{"object_key":"customer"}}`},
		{name: "schema only", body: `{"intent":"List available metrics","metric_spec":{}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.agentDialogAnalysisQuery(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/analysis/query", strings.NewReader(test.body)))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"validated"`) || !strings.Contains(response.Body.String(), `"execution_mode":"gateway_validation"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if result.audits != len(tests) {
		t.Fatalf("validation audits=%d want=%d", result.audits, len(tests))
	}
	if !strings.Contains(resultBodyForAnalysis(t, handler, `{"intent":"Masked fields","dry_run":true,"metric_spec":{"object_key":"customer"}}`), `"masked_fields":["secret"]`) {
		t.Fatal("masked field projection missing")
	}
}

func resultBodyForAnalysis(t *testing.T, handler *AgentDialogHandler, body string) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.agentDialogAnalysisQuery(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/analysis/query", strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("analysis status=%d body=%s", response.Code, response.Body.String())
	}
	return response.Body.String()
}

func TestAgentDialogAnalysisMaskedFieldsAndResultErrors(t *testing.T) {
	unknown, unknownResult := agentAnalysisHandler(newAgentDialogStateRepository(), principalmodel.Principal{})
	response := httptest.NewRecorder()
	unknown.agentDialogAnalysisQuery(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/analysis/query", strings.NewReader(`{"intent":"query","dry_run":true,"metric_spec":{"object_key":"customer"}}`)))
	if response.Code != http.StatusUnprocessableEntity || unknownResult.serviceErrors != 1 {
		t.Fatalf("masked-fields error status=%d serviceErrors=%d", response.Code, unknownResult.serviceErrors)
	}

	handler, _ := agentAnalysisHandler(newAgentDialogStateRepository(), agentAnalysisPrincipal())
	fields, err := handler.agentAnalysisMaskedFields(t.Context(), " ", agentAnalysisPrincipal())
	if err != nil || len(fields) != 0 {
		t.Fatalf("empty object masked fields=%#v err=%v", fields, err)
	}
	principal := agentAnalysisPrincipal()
	accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
		role.FieldPolicies = append(role.FieldPolicies, accessfixture.FieldPolicyFixture{ObjectKey: "order", FieldKey: "secret", Read: true, Masked: true})
	})
	handler.analysisCatalog = metadataapplication.NewMetadataSchemaApplicationService(agentAnalysisSchemaProvider{snapshot: metadatamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
		{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "secret", Type: "text"}}},
		{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "secret", Type: "text"}}},
	}}}, nil)
	fields, err = handler.agentAnalysisMaskedFields(t.Context(), "customer", principal)
	if err != nil || len(fields) != 1 || fields[0] != "secret" {
		t.Fatalf("cross-object masked fields=%#v err=%v", fields, err)
	}
}

func TestAgentDialogWritesScopedAnalysisResultAndGovernance(t *testing.T) {
	repository := newAgentDialogStateRepository()
	handler, result := agentAnalysisHandler(repository, agentAnalysisPrincipal())
	response := httptest.NewRecorder()
	handler.writeAgentAnalysisResult(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/analysis/query", nil), agentAnalysisPrincipal(), "Customers", "query-1", "customer", "customer.summary", []string{"secret"}, nil, map[string]any{"query_ref": "query-1"}, recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Alice"}}}, Total: 2, HasNext: true})
	if response.Code != http.StatusOK || result.audits != 1 || !strings.Contains(response.Body.String(), `"status":"completed"`) || !strings.Contains(response.Body.String(), `"truncated":true`) || !strings.Contains(response.Body.String(), `"customer-1"`) {
		t.Fatalf("result status=%d audits=%d body=%s", response.Code, result.audits, response.Body.String())
	}
	state := agentapplication.NewAgentApplicationService(repository)
	query, err := state.GetReportQueryRun(t.Context(), "query-1", agentAnalysisPrincipal())
	if err != nil || query.RowCount != 1 || query.Total != 2 || !query.Truncated {
		t.Fatalf("query governance=%#v err=%v", query, err)
	}

	repository.putErr = context.Canceled
	failure := httptest.NewRecorder()
	handler.writeAgentAnalysisResult(failure, httptest.NewRequest(http.MethodPost, "/agent-dialog/analysis/query", nil), agentAnalysisPrincipal(), "Customers", "query-2", "customer", "customer.summary", nil, nil, map[string]any{}, recordmodel.RecordPageResult{})
	if failure.Code != http.StatusOK || !strings.Contains(failure.Body.String(), `"report_governance":null`) {
		t.Fatalf("governance failure response status=%d body=%s", failure.Code, failure.Body.String())
	}
}

func TestAgentDialogReportGovernanceHandlersAndDownloadHandoff(t *testing.T) {
	repository := newAgentDialogStateRepository()
	handler, result := agentAnalysisHandler(repository, agentAnalysisPrincipal())
	if refs := handler.agentDialogRecordReportGovernance(t.Context(), " ", "report", "customer", "query", 0, 0, false, "audit", "workspace-1", "analyst", "analyst"); refs != nil {
		t.Fatalf("empty query governance refs=%#v", refs)
	}
	refs := handler.agentDialogRecordReportGovernance(t.Context(), "query-governed", "customer.summary", "customer", "scoped_record_query", 2, 2, false, "audit-query", "workspace-1", "analyst", "analyst")
	if refs["report_query_run_id"] != "query-governed" || refs["download_task_id"] != "query-governed" {
		t.Fatalf("governance refs=%#v", refs)
	}

	tests := []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
	}{
		{name: "query run", call: handler.agentDialogGetReportQueryRun},
		{name: "export audit", call: handler.agentDialogGetReportExportAudit},
		{name: "download task", call: handler.agentDialogGetReportDownloadTask},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/agent-dialog/report/query-governed", nil)
			request.SetPathValue("queryRef", "query-governed")
			response := httptest.NewRecorder()
			test.call(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "query-governed") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	prepareRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/report/query-governed/handoff", nil)
	prepareRequest.SetPathValue("queryRef", " query-governed ")
	prepared := httptest.NewRecorder()
	handler.agentDialogPrepareReportDownloadHandoff(prepared, prepareRequest)
	if prepared.Code != http.StatusOK || result.audits != 1 || !strings.Contains(prepared.Body.String(), `"status":"prepared_for_report_center"`) {
		t.Fatalf("handoff status=%d audits=%d body=%s", prepared.Code, result.audits, prepared.Body.String())
	}

	for _, call := range []func(http.ResponseWriter, *http.Request){handler.agentDialogGetReportQueryRun, handler.agentDialogGetReportExportAudit, handler.agentDialogGetReportDownloadTask, handler.agentDialogPrepareReportDownloadHandoff} {
		request := httptest.NewRequest(http.MethodGet, "/agent-dialog/report/missing", nil)
		request.SetPathValue("queryRef", "missing")
		response := httptest.NewRecorder()
		call(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("missing governance status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if result.serviceErrors != 4 {
		t.Fatalf("governance service errors=%d", result.serviceErrors)
	}
}

func TestAgentDialogAnalysisQueryUsesScopedRecordOwnerAndPropagatesFailure(t *testing.T) {
	records := &agentAnalysisRecordRepository{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Alice"}}}, Total: 1}}
	handler, result := agentAnalysisHandler(newAgentDialogStateRepository(), agentAnalysisPrincipal())
	handler.analysisRecords = agentAnalysisRecordApplication(records)
	body := `{"intent":"Active customers","max_rows":5,"metric_spec":{"object_key":"customer","filters":{"status":"active"}}}`
	response := httptest.NewRecorder()
	handler.agentDialogAnalysisQuery(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/analysis/query", strings.NewReader(body)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"completed"`) || !strings.Contains(response.Body.String(), "customer-1") {
		t.Fatalf("record query status=%d body=%s", response.Code, response.Body.String())
	}
	if records.query.Page != 1 || records.query.PageSize != 5 || records.query.Filters["status"] != "active" {
		t.Fatalf("record query=%#v", records.query)
	}

	records.err = context.Canceled
	failure := httptest.NewRecorder()
	handler.agentDialogAnalysisQuery(failure, httptest.NewRequest(http.MethodPost, "/agent-dialog/analysis/query", strings.NewReader(body)))
	if failure.Code != http.StatusUnprocessableEntity || result.serviceErrors != 1 {
		t.Fatalf("record failure status=%d serviceErrors=%d", failure.Code, result.serviceErrors)
	}
}
