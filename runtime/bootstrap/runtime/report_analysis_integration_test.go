package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	ormschema "github.com/domainry/domainry-orm/schema"
	reportsdk "github.com/domainry/domainry-report-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	reportpersistence "github.com/domainry/domainry-report-sdk/persistence"
	reportmodule "github.com/domainry/domainry-report/module"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	recordstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type analysisUnusedSideEffects struct{}

func (analysisUnusedSideEffects) CompleteReportSnapshot(context.Context, reportpersistence.SnapshotCompleteRequest, notificationmodel.NotificationIntent) error {
	return fmt.Errorf("unexpected snapshot write")
}
func (analysisUnusedSideEffects) FailReportSnapshot(context.Context, reportpersistence.SnapshotFailRequest, notificationmodel.NotificationIntent) error {
	return fmt.Errorf("unexpected snapshot write")
}
func (analysisUnusedSideEffects) PrepareReportExport(context.Context, model.ReportExportPrepareRequest, model.ReportSchema, model.ReportExportControlSchema, model.ReportSubject) (model.ReportExportJob, error) {
	return model.ReportExportJob{}, fmt.Errorf("unexpected export")
}

func TestReportAnalysisRealOwnerFullSQLiteAggregationAndCurrentPermissions(t *testing.T) {
	store := openAgentBindingRuntimeStore(t)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	columns := []ormschema.ColumnDefinition{}
	for _, key := range []string{"workspace_id", "id", "created_at", "updated_at", "owner_user_id", "department", "amount", "occurred_at"} {
		columns = append(columns, ormschema.Column(key, ormschema.Text()))
	}
	columns = append(columns, ormschema.Column("quantity", ormschema.Integer()))
	ddl, args, err := ormschema.NewTable(store.RuntimeRenderer(), "analysis_sale").Columns(columns...).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), ddl, args...); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "analysis_sale", Name: "Sales", Fields: []definitionmodel.FieldSchema{
		{Key: "department", Type: "text"}, {Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2, "currency_code": "CNY"}},
		{Key: "quantity", Type: "integer"}, {Key: "occurred_at", Type: "datetime"},
	}}
	objects := map[string]definitionmodel.ObjectSchema{object.Key: object}
	repository := recordstore.NewRecordStore(store)
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{objects[object.Key]} }})
	application := recordapplication.NewRecordApplicationService(recordapplication.RecordApplicationDependencies{Repository: repository, QueryPolicy: policy, SchemaMap: func() map[string]definitionmodel.ObjectSchema { return objects }, IdentityProfileExtensions: func() []profilebindingmodel.Binding { return nil }})
	access := reportadapter.NewReportRecordAdapter(application, repository, func() map[string]definitionmodel.ObjectSchema { return objects })
	permissions := []string{reportsdk.ActionReportQueryExecute, object.Key + ".read"}
	bundle := accessfixture.Bundle{Key: "analysis-owner", Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, "owner")}
	for _, field := range object.Fields {
		bundle.FieldPolicies = append(bundle.FieldPolicies, accessfixture.FieldPolicyFixture{ObjectKey: object.Key, FieldKey: field.Key, Read: true})
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, bundle)
	sqlStore := reportstore.NewReportSQLStore(store)
	host := reportadapter.NewReportModuleQueryHost(reportadapter.ReportModuleQueryHostDependencies{
		Access: access, ObjectSQL: sqlStore, SnapshotSources: sqlStore, AnalysisObjectKeys: func() []string { return []string{object.Key} },
		ResolveSubject: func(context.Context, model.ReportAuthority) (principalmodel.Principal, error) { return principal, nil },
	})
	open := func() reportsdk.Analyses {
		t.Helper()
		binding, err := reportmodule.NewFactory().Open(t.Context(), reportsdk.ApplicationRef{RuntimeID: "analysis-runtime"}, runtimeReportModuleHost{store: store})
		if err != nil {
			t.Fatal(err)
		}
		err = binding.(reportsdk.ApplicationHostBinder).BindApplicationHost(runtimeReportApplicationHost{runtimeReportModuleHost: runtimeReportModuleHost{store: store}, cursorKey: []byte("analysis-stable-signing-key"), ports: composition.ReportModuleApplicationPorts{Subjects: host, ObjectSQL: host, SourceVersions: host, Audit: host, Authorization: host, Terminals: analysisUnusedSideEffects{}, Exports: analysisUnusedSideEffects{}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = binding.Close(t.Context()) })
		analyses, ok := binding.(reportsdk.ApplicationBinding).Queries().(reportsdk.Analyses)
		if !ok {
			t.Fatal("owner optional analysis API missing")
		}
		return analyses
	}
	owner := open()
	authority := model.ReportAuthority{AccessToken: "in-process-fixture"}
	insert := func(workspace, id, user string, data map[string]any) recordmodel.Record {
		t.Helper()
		record := recordmodel.Record{ID: id, OwnerUserID: user, CreatedAt: "2026-08-01T00:00:00Z", UpdatedAt: "2026-08-01T00:00:00Z", Data: data}
		if err := repository.InsertRecord(t.Context(), workspace, object, record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	for i := 0; i < 1201; i++ {
		insert("workspace-a", fmt.Sprintf("sale-%04d", i), "user-a", map[string]any{"department": "sales", "amount": "0.01", "quantity": 1, "occurred_at": fmt.Sprintf("2026-08-%02dT01:00:00Z", i%2+1)})
	}
	insert("workspace-a", "null", "user-a", map[string]any{"quantity": 0})
	big := insert("workspace-a", "big", "user-a", map[string]any{"department": "big", "amount": "9007199254740993.01", "quantity": 3, "occurred_at": "2026-08-02T01:00:00Z"})
	insert("workspace-a", "other-owner", "user-b", map[string]any{"department": "sales", "amount": "999.99", "quantity": 99, "occurred_at": "2026-08-03T01:00:00Z"})
	foreign := insert("workspace-b", "foreign", "user-a", map[string]any{"department": "sales", "amount": "999.99", "quantity": 99, "occurred_at": "2026-08-03T01:00:00Z"})
	filter := func(department string) []model.AnalysisFilter {
		return []model.AnalysisFilter{{Field: "department", Operator: "eq", Values: []any{department}}}
	}
	request := model.AnalysisRequest{DatasetKey: object.Key, GroupBy: []string{"department"}, Filters: filter("sales"), Measures: []model.AnalysisMeasure{{Key: "revenue", Field: "amount", Function: "sum"}, {Key: "mean", Field: "amount", Function: "avg"}, {Key: "rows", Function: "count"}}}
	run := func(request model.AnalysisRequest) model.AnalysisResult {
		t.Helper()
		result, err := owner.RunAnalysis(t.Context(), request, authority)
		if err != nil {
			for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(cause) {
				t.Logf("analysis cause: %T: %v", cause, cause)
			}
			t.Fatal(err)
		}
		if !result.Source.Complete || result.Source.Proof == "" {
			t.Fatal(result)
		}
		return result
	}
	cell := func(row model.AnalysisRow, key, want string) {
		t.Helper()
		value := row.Values[key]
		if value == nil || *value != want {
			t.Fatalf("%s=%v want=%s; row=%+v", key, value, want, row)
		}
	}
	aggregate := run(request)
	if len(aggregate.Rows) != 1 || aggregate.Source.InputCounts["dataset"] != "1201" {
		t.Fatal(aggregate)
	}
	cell(aggregate.Rows[0], "revenue", "12.01")
	cell(aggregate.Rows[0], "mean", "0.010000")
	cell(aggregate.Rows[0], "rows", "1201")
	if aggregate.Rows[0].NonNullCounts["mean"] != "1201" {
		t.Fatal(aggregate)
	}
	for _, column := range aggregate.Columns {
		if column.Key == "revenue" && column.Unit != "CNY" {
			t.Fatal(column)
		}
	}
	all := request
	all.Filters = nil
	whole := run(all)
	if len(whole.Rows) != 3 || whole.Source.InputCounts["dataset"] != "1203" {
		t.Fatal("source sampled or scope leaked", whole)
	}
	nulls := 0
	for _, row := range whole.Rows {
		if row.Values["department"] == nil {
			nulls++
			if row.Values["mean"] != nil || row.Values["revenue"] != nil {
				t.Fatal("NULL flattened", row)
			}
		}
	}
	if nulls != 1 {
		t.Fatal(whole)
	}
	all.MaxRows = 2
	if result, err := owner.RunAnalysis(t.Context(), all, authority); err == nil || result.Source.Proof != "" {
		t.Fatal("partial groups accepted", result, err)
	}
	compare := request
	compare.Mode = "compare"
	compare.GroupBy = nil
	compare.Filters = nil
	compare.Comparison = &model.AnalysisComparison{Baseline: filter("sales"), Current: filter("big")}
	comparison := run(compare)
	cell(comparison.Rows[0], "baseline_rows", "1201")
	cell(comparison.Rows[0], "current_rows", "1")
	cell(comparison.Rows[0], "current_revenue", "9007199254740993.01")
	trend := request
	trend.Mode = "trend"
	trend.Time = &model.AnalysisTimeBucket{Field: "occurred_at", Grain: "day", TimeZone: "UTC"}
	trending := run(trend)
	if len(trending.Rows) != 2 {
		t.Fatal(trending)
	}
	cell(trending.Rows[0], "revenue", "6.01")
	cell(trending.Rows[1], "delta_revenue", "-0.010000")
	table := model.AnalysisRequest{DatasetKey: object.Key, Mode: "table", Filters: filter("big"), Select: []string{"amount", "quantity"}, Calculations: []model.AnalysisCalculation{{Key: "price", Scale: 6, Expression: model.AnalysisExpression{Operator: "divide", Arguments: []model.AnalysisExpression{{Reference: "amount"}, {Reference: "quantity"}}}}}, AnomalyRules: []model.AnalysisAnomalyRule{{Key: "large", Column: "price", Operator: "gt", Values: []string{"1000000"}}}}
	calculated := run(table)
	cell(calculated.Rows[0], "price", "3002399751580331.003333")
	if len(calculated.Rows[0].Anomalies) != 1 {
		t.Fatal(calculated)
	}
	if err := open().AuthorizeAnalysisResult(t.Context(), model.AnalysisResultAuthorization{Request: table, Result: calculated}, authority); err != nil {
		t.Fatal("owner reopen", err)
	}
	foreign.Data["amount"] = "777.77"
	if err := repository.UpdateRecord(t.Context(), "workspace-b", object, foreign); err != nil {
		t.Fatal(err)
	}
	if err := owner.AuthorizeAnalysisResult(t.Context(), model.AnalysisResultAuthorization{Request: table, Result: calculated}, authority); err != nil {
		t.Fatal("foreign workspace affected source proof", err)
	}
	// Same timestamp and row count, different content: the N02 content
	// fingerprint must invalidate the old result, unlike a MAX watermark.
	big.Data["amount"] = "9007199254740993.02"
	if err := repository.UpdateRecord(t.Context(), "workspace-a", object, big); err != nil {
		t.Fatal(err)
	}
	if err := owner.AuthorizeAnalysisResult(t.Context(), model.AnalysisResultAuthorization{Request: table, Result: calculated}, authority); err == nil {
		t.Fatal("same-time update preserved stale proof")
	}
	fresh := run(table)
	if fresh.Source.DataVersion == calculated.Source.DataVersion {
		t.Fatal("content version unchanged")
	}
	// Readable but masked fields cannot appear in either discovery or plans.
	denied := bundle
	denied.FieldPolicies = append([]accessfixture.FieldPolicyFixture{}, bundle.FieldPolicies...)
	for i := range denied.FieldPolicies {
		if denied.FieldPolicies[i].FieldKey == "amount" {
			denied.FieldPolicies[i].Masked = true
		}
	}
	principal = accessfixture.Attach(principal, denied)
	catalog, err := owner.AnalysisCatalog(t.Context(), model.AnalysisCatalogRequest{}, authority)
	if err != nil {
		t.Fatal(err)
	}
	for _, dataset := range catalog.Datasets {
		for _, column := range dataset.Columns {
			if column.Key == "amount" {
				t.Fatal("masked field discovered")
			}
		}
	}
	if _, err := owner.RunAnalysis(t.Context(), table, authority); err == nil {
		t.Fatal("masked field aggregated")
	}
	if err := owner.AuthorizeAnalysisResult(t.Context(), model.AnalysisResultAuthorization{Request: table, Result: fresh}, authority); err == nil {
		t.Fatal("revoked result remained readable")
	}
	principal = accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "nobody"}}, bundle)
	emptyRequest := model.AnalysisRequest{DatasetKey: object.Key, Measures: []model.AnalysisMeasure{{Key: "rows", Function: "count"}, {Key: "mean", Field: "amount", Function: "avg"}}}
	empty := run(emptyRequest)
	cell(empty.Rows[0], "rows", "0")
	if empty.Source.InputCounts["dataset"] != "0" || empty.Rows[0].Values["mean"] != nil {
		t.Fatal(empty)
	}
	t.Logf("real SQLite: 1203 authorized input rows, 1201 matching rows, 2 excluded tenant/owner rows; aggregation, comparison, trend, table/rules, NULL, overflow, same-time version, owner reopen and field revocation passed")
}
