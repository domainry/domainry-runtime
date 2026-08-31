package record_test

import (
	"context"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

func TestRelationAwareRLSEndToEndUsesDatabaseForListTotalDetailAndReverseExistence(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	objects := relationRLSObjects()
	for _, object := range objects {
		createRelationRLSTable(t, store, object)
	}
	repository := recordStore(store)
	insertRelationRLSRecord(t, repository, objects[3], "member-1", map[string]any{"name": "Alice"})
	insertRelationRLSRecord(t, repository, objects[3], "member-2", map[string]any{"name": "Bob"})
	insertRelationRLSRecord(t, repository, objects[2], "card-1", map[string]any{"member_id": "member-1"})
	insertRelationRLSRecord(t, repository, objects[2], "card-2", map[string]any{"member_id": "member-2"})
	insertRelationRLSRecord(t, repository, objects[1], "account-1", map[string]any{"card_id": "card-1"})
	insertRelationRLSRecord(t, repository, objects[1], "account-2", map[string]any{"card_id": "card-2"})
	insertRelationRLSRecord(t, repository, objects[0], "ledger-1", map[string]any{"account_id": "account-1", "status": "posted", "description": "member one"})
	insertRelationRLSRecord(t, repository, objects[0], "ledger-2", map[string]any{"account_id": "account-2", "status": "posted", "description": "member two"})
	insertRelationRLSRecord(t, repository, objects[0], "ledger-3", map[string]any{"account_id": "account-1", "status": "voided", "description": "excluded"})
	insertRelationRLSRecord(t, repository, objects[4], "package-1", map[string]any{"member_id": "member-1", "coach_id": "coach-1"})
	insertRelationRLSRecord(t, repository, objects[4], "package-2", map[string]any{"member_id": "member-2", "coach_id": "coach-2"})
	insertRelationRLSRecord(t, repository, objects[6], "student-1", map[string]any{"name": "Student One"})
	insertRelationRLSRecord(t, repository, objects[6], "student-2", map[string]any{"name": "Student Two"})
	insertRelationRLSRecord(t, repository, objects[7], "training-package-1", map[string]any{"student_id": "student-1"})
	insertRelationRLSRecord(t, repository, objects[7], "training-package-2", map[string]any{"student_id": "student-2"})
	insertRelationRLSRecord(t, repository, objects[8], "training-session-1", map[string]any{"package_id": "training-package-1", "status": "scheduled"})
	insertRelationRLSRecord(t, repository, objects[8], "training-session-2", map[string]any{"package_id": "training-package-2", "status": "scheduled"})

	memberPredicate := &accessfixture.PredicateFixture{Operator: "and", Children: []accessfixture.PredicateFixture{
		{Operator: "in", FieldKey: "status", ValueSource: "literal", Values: []string{"posted", "settled"}},
		{Operator: "eq", Path: []accessfixture.RelationSegmentFixture{{Direction: "forward", RelationFieldKey: "account_id", TargetObjectKey: "account"}, {Direction: "forward", RelationFieldKey: "card_id", TargetObjectKey: "card"}, {Direction: "forward", RelationFieldKey: "member_id", TargetObjectKey: "member"}}, FieldKey: "id", ValueSource: "actor_claim", ClaimKey: "business_profile_id"},
	}}
	cardPredicate := &accessfixture.PredicateFixture{Operator: "eq", Path: []accessfixture.RelationSegmentFixture{{Direction: "forward", RelationFieldKey: "member_id", TargetObjectKey: "member"}}, FieldKey: "id", ValueSource: "actor_claim", ClaimKey: "business_profile_id"}
	accountPredicate := &accessfixture.PredicateFixture{Operator: "eq", Path: []accessfixture.RelationSegmentFixture{{Direction: "forward", RelationFieldKey: "card_id", TargetObjectKey: "card"}, {Direction: "forward", RelationFieldKey: "member_id", TargetObjectKey: "member"}}, FieldKey: "id", ValueSource: "actor_claim", ClaimKey: "business_profile_id"}
	memberRole := accessfixture.Bundle{
		Key: "member", Permissions: []string{"ledger.read", "ledger.export", "account.read", "account.export", "card.read", "card.export"}, RecordScope: "custom",
		DataPolicies: []accessfixture.DataPolicyFixture{
			{ObjectKey: "ledger", Scope: "custom", Read: true, Predicate: memberPredicate},
			{ObjectKey: "account", Scope: "custom", Read: true, Predicate: accountPredicate},
			{ObjectKey: "card", Scope: "custom", Read: true, Predicate: cardPredicate},
		},
	}
	memberPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary", UserID: "identity-member-1"}, ActiveBusinessProfile: &profilebindingmodel.Reference{RecordID: "member-1"}}, memberRole)
	compiled, err, handled := recordservice.RecordCompileSDKDataScopeExpression(objects[0], objects, memberPrincipal, "read")
	if err != nil || !handled || compiled == nil {
		t.Fatalf("compile SDK record scope handled=%v expression=%#v err=%v", handled, compiled, err)
	}
	for _, test := range []struct {
		accountID string
		want      bool
	}{
		{accountID: "account-1", want: true},
		{accountID: "account-2", want: false},
	} {
		matched, matchErr := repository.CandidateScopeMatches(t.Context(), "workspace-primary", recordmodel.Record{ID: "ledger-new", Data: map[string]any{"account_id": test.accountID, "status": "posted"}}, *compiled)
		if matchErr != nil || matched != test.want {
			t.Fatalf("candidate account=%s matched=%v want=%v err=%v", test.accountID, matched, test.want, matchErr)
		}
	}
	var permissionLookupSQL string
	var permissionLookupArgs []any
	resolved, err := querypersistence.ResolveScopeMembership(store, "workspace-primary", *compiled, 1000, func(statement string, args ...any) ([]string, error) {
		permissionLookupSQL = statement
		permissionLookupArgs = append([]any(nil), args...)
		rows, queryErr := store.DB().Query(statement, args...)
		if queryErr != nil {
			return nil, queryErr
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				return nil, scanErr
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	permissionPlan := sqliteExplainQueryPlan(t, store, permissionLookupSQL, permissionLookupArgs...)
	for _, index := range []string{"sqlite_autoindex_member_1", "idx_card_member_id", "idx_account_card_id"} {
		if !strings.Contains(permissionPlan, index) {
			t.Fatalf("permission lookup did not use %s:\n%s", index, permissionPlan)
		}
	}
	rootWhere, _, err := store.TenantListWhereClause("workspace-primary", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "ledger", ScopeExpression: &resolved})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rootWhere, `"ledger"."account_id" IN (`) || strings.Contains(rootWhere, "JOIN") || strings.Contains(rootWhere, "EXISTS") {
		t.Fatalf("root business query must consume resolved account IDs only: %s", rootWhere)
	}
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return objects }})
	reader := recordservice.NewRecordReadDomainService(recordservice.RecordReadDependencies{Repository: repository, Policy: policy})
	page, err := reader.ListRecords(t.Context(), "ledger", recordmodel.RecordListQuery{Page: 1, PageSize: 1, Search: "member", Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}}, memberPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "ledger-1" || page.HasNext {
		t.Fatalf("member list/page total leaked scope: %#v", page)
	}
	if record, err := reader.GetRecord(t.Context(), "ledger", "ledger-1", memberPrincipal); err != nil || record.ID != "ledger-1" {
		t.Fatalf("allowed detail=%#v err=%v", record, err)
	}
	writerRole := memberRole
	writerRole.Permissions = append(append([]string{}, writerRole.Permissions...), "ledger.update")
	for index := range writerRole.DataPolicies {
		if writerRole.DataPolicies[index].ObjectKey == "ledger" {
			writerRole.DataPolicies[index].Write = true
		}
	}
	writer := accessfixture.Attach(memberPrincipal, writerRole)
	contextualPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects:               func() []definitionmodel.ObjectSchema { return objects },
		CandidateScopeMatches: repository.CandidateScopeMatches,
	})
	updater := recordapplication.NewRecordUpdateApplicationService(recordapplication.RecordUpdateDependencies{
		Repository:      repository,
		ObjectForAction: contextualPolicy.ObjectForAction,
		CanAccess:       contextualPolicy.CanAccessRecord,
		CanWrite:        contextualPolicy.CanWriteRecordScope,
		CanAccessScope:  contextualPolicy.CanAccessPersistedRecordScope,
	})
	updated, err := updater.Update(t.Context(), "ledger", "ledger-1", map[string]any{
		"description": "member one updated",
	}, writer)
	if err != nil || updated.Data["description"] != "member one updated" {
		t.Fatalf("relation-scoped update=%#v err=%v", updated, err)
	}
	updated, err = updater.ConditionalUpdate(t.Context(), "ledger", "ledger-1", transactionmodel.ConditionalUpdateInput{
		Predicates: []transactionmodel.MutationPredicate{{Field: "status", Operator: "eq", Value: "posted"}},
		Patch:      map[string]any{"description": "member one conditionally updated"},
	}, writer)
	if err != nil || updated.Data["description"] != "member one conditionally updated" {
		t.Fatalf("relation-scoped conditional update=%#v err=%v", updated, err)
	}
	if _, err := reader.GetRecord(t.Context(), "ledger", "ledger-2", memberPrincipal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("out-of-scope detail was not concealed: %v", err)
	}
	objectMap := map[string]definitionmodel.ObjectSchema{}
	for _, object := range objects {
		objectMap[object.Key] = object
	}
	exporter := recordapplication.NewRecordExportApplicationService(recordapplication.RecordExportDependencies{
		Repository:     repository,
		Objects:        func() map[string]definitionmodel.ObjectSchema { return objectMap },
		NormalizeQuery: policy.NormalizeListQuery,
		CanAccess:      policy.CanAccessRecord,
		ListRecords:    reader.ListRecords,
	})
	csv, _, err := exporter.Export(t.Context(), "ledger", memberPrincipal)
	if err != nil || !strings.Contains(string(csv), "ledger-1") || strings.Contains(string(csv), "ledger-2") || strings.Contains(string(csv), "ledger-3") {
		t.Fatalf("export did not preserve database RLS: csv=%s err=%v", csv, err)
	}
	reportDefinition := reportmodel.ReportSchema{Key: "member-ledger", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "ledger", Alias: "ledger"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "status", Field: reportmodel.ReportDatasetField{SourceAlias: "ledger", FieldKey: "status"}}}}}
	sources := readAuthorizedReportSources(t, reportDefinition, memberPrincipal, relationRLSReportAccess{policy: policy}, relationRLSReportRecords{repository: repository})
	if len(sources.Records["ledger"]) != 1 {
		t.Fatalf("Report host did not preserve RLS downpush: sources=%#v", sources)
	}
	assertRelationScopeAcrossReadExportAndReport(t, reader, exporter, policy, repository, memberPrincipal, "card", "card-1", "card-2", "member_id")
	assertRelationScopeAcrossReadExportAndReport(t, reader, exporter, policy, repository, memberPrincipal, "account", "account-1", "account-2", "card_id")

	coachPredicate := &accessfixture.PredicateFixture{Operator: "eq", Path: []accessfixture.RelationSegmentFixture{{Direction: "reverse", RelationFieldKey: "member_id", TargetObjectKey: "package"}}, FieldKey: "coach_id", ValueSource: "actor_claim", ClaimKey: "business_profile_id"}
	coachPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}, ActiveBusinessProfile: &profilebindingmodel.Reference{RecordID: "coach-1"}}, accessfixture.Bundle{Key: "coach", Permissions: []string{"member.read"}, RecordScope: "custom", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "member", Scope: "custom", Read: true, Predicate: coachPredicate}}})
	page, err = reader.ListRecords(t.Context(), "member", recordmodel.RecordListQuery{Page: 1, PageSize: 20}, coachPrincipal)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "member-1" {
		t.Fatalf("reverse existence scope page=%#v err=%v", page, err)
	}

	missingProfile := memberPrincipal
	missingProfile.ActiveBusinessProfile = nil
	page, err = reader.ListRecords(t.Context(), "ledger", recordmodel.RecordListQuery{Page: 1, PageSize: 20}, missingProfile)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("missing server-resolved profile must deny all: page=%#v err=%v", page, err)
	}

	studentPredicate := &accessfixture.PredicateFixture{Operator: "eq", Path: []accessfixture.RelationSegmentFixture{{Direction: "forward", RelationFieldKey: "package_id", TargetObjectKey: "training_package"}, {Direction: "forward", RelationFieldKey: "student_id", TargetObjectKey: "student"}}, FieldKey: "id", ValueSource: "actor_claim", ClaimKey: "business_profile_id"}
	studentPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}, ActiveBusinessProfile: &profilebindingmodel.Reference{RecordID: "student-1"}}, accessfixture.Bundle{Key: "student", Permissions: []string{"training_session.read", "training_session.export"}, RecordScope: "custom", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "training_session", Scope: "custom", Read: true, Predicate: studentPredicate}}})
	page, err = reader.ListRecords(t.Context(), "training_session", recordmodel.RecordListQuery{Page: 1, PageSize: 20}, studentPrincipal)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "training-session-1" {
		t.Fatalf("session-package-student list scope mismatch: page=%#v err=%v", page, err)
	}
	if _, err := reader.GetRecord(t.Context(), "training_session", "training-session-2", studentPrincipal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("session-package-student detail was not concealed: %v", err)
	}
	csv, _, err = exporter.Export(t.Context(), "training_session", studentPrincipal)
	if err != nil || !strings.Contains(string(csv), "training-session-1") || strings.Contains(string(csv), "training-session-2") {
		t.Fatalf("session-package-student export scope mismatch: csv=%s err=%v", csv, err)
	}
	sessionDefinition := reportmodel.ReportSchema{Key: "student-sessions", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "training_session", Alias: "training_session"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "status", Field: reportmodel.ReportDatasetField{SourceAlias: "training_session", FieldKey: "status"}}}}}
	sources = readAuthorizedReportSources(t, sessionDefinition, studentPrincipal, relationRLSReportAccess{policy: policy}, relationRLSReportRecords{repository: repository})
	if len(sources.Records["training_session"]) != 1 {
		t.Fatalf("session-package-student Report host scope mismatch: sources=%#v", sources)
	}
}

func assertRelationScopeAcrossReadExportAndReport(t *testing.T, reader *recordservice.RecordReadDomainService, exporter *recordapplication.RecordExportApplicationService, policy *recordservice.RecordQueryPolicyDomainService, repository recordrepository.RecordRepository, principal principalmodel.Principal, objectKey, allowedID, deniedID, reportField string) {
	t.Helper()
	page, err := reader.ListRecords(t.Context(), objectKey, recordmodel.RecordListQuery{Page: 1, PageSize: 20}, principal)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != allowedID {
		t.Fatalf("%s list scope mismatch: page=%#v err=%v", objectKey, page, err)
	}
	if _, err := reader.GetRecord(t.Context(), objectKey, deniedID, principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("%s detail scope mismatch: %v", objectKey, err)
	}
	csv, _, err := exporter.Export(t.Context(), objectKey, principal)
	if err != nil || !strings.Contains(string(csv), allowedID) || strings.Contains(string(csv), deniedID) {
		t.Fatalf("%s export scope mismatch: csv=%s err=%v", objectKey, csv, err)
	}
	reportDefinition := reportmodel.ReportSchema{Key: objectKey + "-scope", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: objectKey, Alias: objectKey}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: reportField, Field: reportmodel.ReportDatasetField{SourceAlias: objectKey, FieldKey: reportField}}}}}
	sources := readAuthorizedReportSources(t, reportDefinition, principal, relationRLSReportAccess{policy: policy}, relationRLSReportRecords{repository: repository})
	if len(sources.Records[objectKey]) != 1 {
		t.Fatalf("%s Report host scope mismatch: sources=%#v", objectKey, sources)
	}
}

type relationRLSReportAccess struct {
	policy *recordservice.RecordQueryPolicyDomainService
}

func (a relationRLSReportAccess) ReportObjectForAction(_ context.Context, principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	return a.policy.ObjectForAction(principal, objectKey, action)
}

func (a relationRLSReportAccess) NormalizeReportListQuery(_ context.Context, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	return a.policy.NormalizeListQuery(object, query, principal)
}

func (a relationRLSReportAccess) CanAccessReportRecord(_ context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return a.policy.CanAccessRecord(principal, object, record)
}

type relationRLSReportRecords struct {
	repository recordrepository.RecordRepository
}

func (r relationRLSReportRecords) ListReportRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return r.repository.ListRecords(ctx, workspaceID, object, query)
}

func relationRLSObjects() []definitionmodel.ObjectSchema {
	relation := func(key, target string) definitionmodel.FieldSchema {
		return definitionmodel.FieldSchema{Key: key, Type: "relation", Validation: definitionmodel.FieldValidation{Target: target}, Config: map[string]any{"indexed": true}}
	}
	return []definitionmodel.ObjectSchema{
		{Key: "ledger", Fields: []definitionmodel.FieldSchema{relation("account_id", "account"), {Key: "status", Type: "select"}, {Key: "description", Type: "text"}}},
		{Key: "account", Fields: []definitionmodel.FieldSchema{relation("card_id", "card")}},
		{Key: "card", Fields: []definitionmodel.FieldSchema{relation("member_id", "member")}},
		{Key: "member", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}},
		{Key: "package", Fields: []definitionmodel.FieldSchema{relation("member_id", "member"), relation("coach_id", "coach")}},
		{Key: "coach", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}},
		{Key: "student", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}},
		{Key: "training_package", Fields: []definitionmodel.FieldSchema{relation("student_id", "student")}},
		{Key: "training_session", Fields: []definitionmodel.FieldSchema{relation("package_id", "training_package"), {Key: "status", Type: "select"}}},
	}
}

func createRelationRLSTable(t *testing.T, store *RuntimeStore, object definitionmodel.ObjectSchema) {
	t.Helper()
	columns := []string{
		store.Identifier("workspace_id") + " TEXT NOT NULL", store.Identifier("id") + " TEXT NOT NULL",
		store.Identifier("created_at") + " TEXT NOT NULL", store.Identifier("updated_at") + " TEXT NOT NULL",
		store.Identifier("deleted") + " BOOLEAN NOT NULL DEFAULT FALSE", store.Identifier("ext_info") + " TEXT NOT NULL DEFAULT '{}'",
		store.Identifier("create_by") + " TEXT", store.Identifier("update_by") + " TEXT",
	}
	for _, field := range object.Fields {
		columns = append(columns, store.Identifier(field.Key)+" TEXT")
	}
	columns = append(columns, "PRIMARY KEY ("+store.Identifier("workspace_id")+", "+store.Identifier("id")+")")
	if _, err := store.DB().Exec("CREATE TABLE " + store.Identifier(object.Key) + " (" + strings.Join(columns, ", ") + ")"); err != nil {
		t.Fatal(err)
	}
	for _, field := range object.Fields {
		if field.Type == "relation" {
			if _, err := store.DB().Exec("CREATE INDEX " + store.Identifier("idx_"+object.Key+"_"+field.Key) + " ON " + store.Identifier(object.Key) + " (" + store.Identifier("workspace_id") + ", " + store.Identifier(field.Key) + ")"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func insertRelationRLSRecord(t *testing.T, repository recordpersistence.RecordStore, object definitionmodel.ObjectSchema, id string, data map[string]any) {
	t.Helper()
	if err := repository.InsertRecord(t.Context(), "workspace-primary", object, recordmodel.Record{ID: id, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: data}); err != nil {
		t.Fatalf("insert %s.%s: %v", object.Key, id, err)
	}
}
