package record_test

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"fmt"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	"strings"
	"testing"
	"time"
)

func TestListRecordsUsesWorkforceResolvedUserIDsForSubordinateScopes(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure runtime schema: %v", err)
	}
	object := reportingScopeTestObject()
	createReportingScopeTable(t, store, object)
	users := []identitysdk.User{
		{ID: "boss"}, {ID: "lead"}, {ID: "member"}, {ID: "outside"},
	}
	for _, user := range users {
		insertReportingScopeRecord(t, store, object, "record_"+user.ID, user.ID)
	}

	subordinates := listReportingScopeRecords(t, store, object, "subordinates", []string{"lead", "member"})
	assertRecordIDs(t, subordinates, "record_lead", "record_member")
	leadScope := listReportingScopeRecords(t, store, object, "subordinates", []string{"member"})
	assertRecordIDs(t, leadScope, "record_member")
}

func TestReportingScopeQueryUsesPersistedPathAndOwnerIndexesAtScale(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure runtime schema: %v", err)
	}
	object := reportingScopeTestObject()
	createReportingScopeTable(t, store, object)
	seedReportingScopeScaleFixture(t, store, 10_000, 10)

	reportingUserIDs := reportingScopeTeamUserIDs(42, 100)
	query := recordmodel.RecordListQuery{Page: 1, PageSize: 20, Scope: "subordinates", PrincipalWorkspaceID: "workspace-primary", PrincipalReportingUserIDs: reportingUserIDs, OwnerField: "owner"}
	whereSQL, args, err := store.TenantListWhereClause("workspace-primary", query)
	if err != nil {
		t.Fatal(err)
	}
	plan := sqliteExplainQueryPlan(t, store, "SELECT COUNT(*) FROM "+store.Identifier(object.Key)+whereSQL, args...)
	if !strings.Contains(plan, "idx_reporting_record_owner") {
		t.Fatalf("reporting scope query must use the owner index, plan:\n%s", plan)
	}

	started := time.Now()
	page, err := recordStore(store).ListRecords(t.Context(), "workspace-primary", object, query)
	if err != nil {
		t.Fatalf("list scale reporting scope: %v", err)
	}
	if page.Total != 1_000 {
		t.Fatalf("expected 1000 records for one 100-person team, got %d", page.Total)
	}
	t.Logf("reporting scope baseline: users=10000 records=100000 matched=%d duration=%s plan=%s", page.Total, time.Since(started), strings.ReplaceAll(plan, "\n", "; "))
}

func seedReportingScopeScaleFixture(t *testing.T, store *RuntimeStore, userCount, recordsPerUser int) {
	t.Helper()
	tx, err := store.DB().Begin()
	if err != nil {
		t.Fatalf("begin scale fixture: %v", err)
	}
	now := "2026-01-01T00:00:00Z"
	recordStatement, err := tx.Prepare(`INSERT INTO reporting_record (workspace_id, id, created_at, updated_at, name, owner) VALUES ('workspace-primary', ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare records: %v", err)
	}
	defer recordStatement.Close()
	for index := 0; index < userCount; index++ {
		userID := fmt.Sprintf("user-%05d", index)
		for recordIndex := 0; recordIndex < recordsPerUser; recordIndex++ {
			recordID := fmt.Sprintf("record-%05d-%02d", index, recordIndex)
			if _, err := recordStatement.Exec(recordID, now, now, recordID, userID); err != nil {
				t.Fatalf("insert scale record %s: %v", recordID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit scale fixture: %v", err)
	}
}

func reportingScopeTeamUserIDs(teamIndex, teamSize int) []string {
	out := make([]string, 0, teamSize)
	for index := teamIndex * teamSize; index < (teamIndex+1)*teamSize; index++ {
		out = append(out, fmt.Sprintf("user-%05d", index))
	}
	return out
}

func sqliteExplainQueryPlan(t *testing.T, store *RuntimeStore, query string, args ...any) string {
	t.Helper()
	rows, err := store.DB().Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain reporting query: %v", err)
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan explain plan: %v", err)
		}
		parts = append(parts, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read explain plan: %v", err)
	}
	return strings.Join(parts, "\n")
}

func reportingScopeTestObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "reporting_record", Name: "Reporting Record", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Name: "Name", Type: "text"},
		{Key: "owner", Name: "Owner", Type: "user"},
	}}
}

func createReportingScopeTable(t *testing.T, store *RuntimeStore, object definitionmodel.ObjectSchema) {
	t.Helper()
	query := "CREATE TABLE " + store.Identifier(object.Key) + " (" +
		store.Identifier("workspace_id") + " TEXT NOT NULL, " + store.Identifier("id") + " TEXT PRIMARY KEY, " + store.Identifier("created_at") + " TEXT NOT NULL, " +
		store.Identifier("updated_at") + " TEXT NOT NULL, " + store.Identifier("name") + " TEXT, " + store.Identifier("owner") + " TEXT)"
	if _, err := store.DB().Exec(query); err != nil {
		t.Fatalf("create reporting table: %v", err)
	}
	if _, err := store.DB().Exec("CREATE INDEX idx_reporting_record_owner ON " + store.Identifier(object.Key) + " (" + store.Identifier("owner") + ")"); err != nil {
		t.Fatalf("create owner index: %v", err)
	}
}

func insertReportingScopeRecord(t *testing.T, store *RuntimeStore, object definitionmodel.ObjectSchema, id, owner string) {
	t.Helper()
	if err := recordStore(store).InsertRecord(t.Context(), "workspace-primary", object, recordmodel.Record{ID: id, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"name": id, "owner": owner}}); err != nil {
		t.Fatalf("insert reporting record %s: %v", id, err)
	}
}

func listReportingScopeRecords(t *testing.T, store *RuntimeStore, object definitionmodel.ObjectSchema, scope string, reportingUserIDs []string) recordmodel.RecordPageResult {
	t.Helper()
	page, err := recordStore(store).ListRecords(t.Context(), "workspace-primary", object, recordmodel.RecordListQuery{Page: 1, PageSize: 20, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}, Scope: scope, PrincipalWorkspaceID: "workspace-primary", PrincipalReportingUserIDs: reportingUserIDs, OwnerField: "owner"})
	if err != nil {
		t.Fatalf("list %s records: %v", scope, err)
	}
	return page
}

func assertRecordIDs(t *testing.T, page recordmodel.RecordPageResult, expected ...string) {
	t.Helper()
	if page.Total != len(expected) || len(page.Items) != len(expected) {
		t.Fatalf("expected %d records, got %#v", len(expected), page)
	}
	for index, record := range page.Items {
		if record.ID != expected[index] {
			t.Fatalf("expected record %s at %d, got %#v", expected[index], index, page.Items)
		}
	}
}
