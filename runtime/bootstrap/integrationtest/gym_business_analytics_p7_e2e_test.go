package integrationtest

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	runtimecomposition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestGymSixDashboardsUseGenericReportRuntimeWithRLSAndSnapshots(t *testing.T) {
	environment := newGymAnalyticsP7Environment(t)
	environment.seedAcceptanceFacts(t)

	manager := environment.principals["manager"]
	summaries := map[string]reportmodel.ReportSummary{}
	for _, reportKey := range gymAnalyticsP7ReportKeys() {
		summary, err := environment.service.Applications().ReportQueries.Summary(t.Context(), reportKey, manager)
		if err != nil {
			t.Fatalf("realtime report %s: %v", reportKey, err)
		}
		if summary.ExecutionMode != "realtime" || summary.SourceRowCount == 0 {
			t.Fatalf("report %s did not execute generic realtime dataset: %#v", reportKey, summary)
		}
		summaries[reportKey] = summary
	}

	assertGymRevenueDashboard(t, summaries["gym_revenue"])
	assertGymRetentionDashboard(t, summaries["gym_member_retention"])
	assertGymAttendanceDashboard(t, summaries["gym_attendance"])
	assertGymEquipmentDashboard(t, summaries["gym_equipment"])
	assertGymTrainerDashboard(t, summaries["gym_trainer_performance"])
	assertGymAcquisitionDashboard(t, summaries["gym_acquisition_funnel"])

	environment.assertReportMatchesOwnedDetail(t, "gym_trainer_performance", "coach", "training")
	environment.assertReportMatchesOwnedDetail(t, "gym_acquisition_funnel", "advisor", "acquisition")

	for _, reportKey := range gymAnalyticsP7ReportKeys() {
		snapshot, err := environment.service.Applications().ReportSnapshots.RefreshSnapshot(t.Context(), reportKey, "p7-"+reportKey, manager)
		if err != nil || snapshot.Status != "succeeded" || snapshot.Watermark == "" || len(snapshot.SourceVersions) == 0 {
			t.Fatalf("refresh %s snapshot=%#v err=%v", reportKey, snapshot, err)
		}
		summary, err := environment.service.Applications().ReportQueries.SummaryMode(t.Context(), reportKey, "snapshot", manager)
		if err != nil || summary.ExecutionMode != "snapshot" || summary.Snapshot == nil || summary.Snapshot.SnapshotID != snapshot.ID || summary.Snapshot.Stale {
			t.Fatalf("snapshot summary %s=%#v err=%v", reportKey, summary, err)
		}
	}
}

type gymAnalyticsP7Environment struct {
	store      *persistence.RuntimeStore
	service    *runtimecomposition.RuntimeServices
	object     definitionmodel.ObjectSchema
	principals map[string]principalmodel.Principal
}

func newGymAnalyticsP7Environment(t *testing.T) *gymAnalyticsP7Environment {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "gym-analytics-p7.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	object := gymAnalyticsP7Object()
	planToProduceCreateTable(t, store, object)
	for _, field := range []string{"metric_type", "entity_id", "event_key", "occurred_at", "owner"} {
		if _, err := store.DB().Exec(`CREATE INDEX "gym_metric_fact_` + field + `" ON "gym_metric_fact" ("workspace_id", "` + field + `")`); err != nil {
			t.Fatalf("create analytics index %s: %v", field, err)
		}
	}
	roles := gymAnalyticsP7Roles()
	principals := make(map[string]principalmodel.Principal, len(roles))
	for _, role := range roles {
		userID := role.Key
		switch role.Key {
		case "coach":
			userID = "trainer-1"
		case "advisor":
			userID = "advisor-1"
		}
		principals[role.Key] = accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID, WorkspaceID: "default"}}, role)
	}
	service := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{
		TemplateID: "gym-analytics-p7-fixture", TemplateVersion: "1", Name: "Gym Analytics P7 Fixture",
		Objects: []definitionmodel.ObjectSchema{object}, Reports: gymAnalyticsP7Reports(),
		Integrations: integrationmodel.IntegrationSchema{}, Store: store,
	})
	return &gymAnalyticsP7Environment{store: store, service: service, object: object, principals: principals}
}

func gymAnalyticsP7Object() definitionmodel.ObjectSchema {
	indexed := func(key, kind string) definitionmodel.FieldSchema {
		return definitionmodel.FieldSchema{Key: key, Name: key, Type: kind, Config: map[string]any{"indexed": true}}
	}
	return definitionmodel.ObjectSchema{Key: "gym_metric_fact", Name: "Gym Metric Fact", Fields: []definitionmodel.FieldSchema{
		indexed("metric_type", "status"), indexed("entity_id", "text"), indexed("event_key", "status"), indexed("occurred_at", "datetime"), indexed("owner", "user"),
		{Key: "category", Name: "category", Type: "text"},
		{Key: "amount", Name: "amount", Type: "currency", Config: map[string]any{"precision": 18, "scale": 2}},
		{Key: "ratio_value", Name: "ratio_value", Type: "decimal", Config: map[string]any{"precision": 18, "scale": 6}},
		{Key: "started_at", Name: "started_at", Type: "datetime"},
		{Key: "ended_at", Name: "ended_at", Type: "datetime"},
	}}
}

func gymAnalyticsP7Roles() []accessfixture.Bundle {
	permission := accessfixture.DataPolicyFixture{ObjectKey: "gym_metric_fact", Scope: "all_records", Read: true, Write: true}
	owned := permission
	owned.Scope = "owned_records"
	return []accessfixture.Bundle{
		{Key: "manager", Permissions: []string{"workspace.admin", "gym_metric_fact.read"}, RecordScope: "all_records", DataPolicies: []accessfixture.DataPolicyFixture{permission}},
		{Key: "coach", Permissions: []string{"gym_metric_fact.read"}, RecordScope: "owned_records", DataPolicies: []accessfixture.DataPolicyFixture{owned}},
		{Key: "advisor", Permissions: []string{"gym_metric_fact.read"}, RecordScope: "owned_records", DataPolicies: []accessfixture.DataPolicyFixture{owned}},
	}
}

func gymAnalyticsP7Reports() []reportmodel.ReportSchema {
	field := gymAnalyticsP7Field
	base := func(key, metricType string) reportmodel.ReportSchema {
		return reportmodel.ReportSchema{
			Key: key, Name: key, RequiredPermissions: []string{"gym_metric_fact.read"},
			Materialization: &reportmodel.ReportMaterializationPolicy{MaximumLagSeconds: 86400, ConsistencyRetries: 3},
			Dataset: reportmodel.ReportDatasetSchema{
				Source:  reportmodel.ReportDatasetSource{ObjectKey: "gym_metric_fact", Alias: "facts"},
				Filters: []reportmodel.ReportDatasetFilter{{Field: *field("metric_type"), Operator: "eq", Value: metricType}},
			},
		}
	}
	revenue := base("gym_revenue", "revenue")
	revenue.Dataset.Dimensions = []reportmodel.ReportDatasetDimension{{Key: "day", Field: *field("occurred_at"), TimeGrain: "day"}, {Key: "payment_method", Field: *field("category")}}
	revenue.Dataset.Measures = []reportmodel.ReportDatasetMeasure{{Key: "orders", Operation: "count", SourceAlias: "facts"}, {Key: "revenue", Operation: "sum", Field: field("amount")}, {Key: "average_ticket", Operation: "avg", Field: field("amount")}}
	revenue.Dataset.Sort = []reportmodel.ReportDatasetSort{{Key: "day", Direction: "asc"}, {Key: "payment_method", Direction: "asc"}}

	retention := base("gym_member_retention", "membership")
	retention.Dataset.Dimensions = []reportmodel.ReportDatasetDimension{{Key: "member_state", Field: *field("event_key")}}
	retention.Dataset.Measures = []reportmodel.ReportDatasetMeasure{{Key: "members", Operation: "distinct_count", Field: field("entity_id")}}
	retention.Dataset.Analyses = []reportmodel.ReportDatasetAnalysis{{Key: "retention", Type: "cohort_retention", EntityField: *field("entity_id"), TimeField: *field("occurred_at"), CohortGrain: "month", PeriodGrain: "month", MaximumPeriods: 6}}

	attendance := base("gym_attendance", "attendance")
	attendance.Dataset.Dimensions = []reportmodel.ReportDatasetDimension{{Key: "hour", Field: *field("occurred_at"), TimeGrain: "hour"}, {Key: "session_type", Field: *field("category")}}
	attendance.Dataset.Measures = []reportmodel.ReportDatasetMeasure{{Key: "visits", Operation: "count", SourceAlias: "facts"}, {Key: "attendance_rate", Operation: "avg", Field: field("ratio_value")}}

	equipment := base("gym_equipment", "equipment")
	equipment.Dataset.Dimensions = []reportmodel.ReportDatasetDimension{{Key: "zone", Field: *field("category")}}
	equipment.Dataset.Measures = []reportmodel.ReportDatasetMeasure{{Key: "events", Operation: "count", SourceAlias: "facts"}, {Key: "utilization", Operation: "avg", Field: field("ratio_value")}, {Key: "repair_seconds", Operation: "duration", StartField: field("started_at"), EndField: field("ended_at")}, {Key: "average_repair_seconds", Operation: "ratio", NumeratorKey: "repair_seconds", DenominatorKey: "events"}}

	trainer := base("gym_trainer_performance", "training")
	trainer.Dataset.Dimensions = []reportmodel.ReportDatasetDimension{{Key: "trainer", Field: *field("owner")}}
	trainer.Dataset.Measures = []reportmodel.ReportDatasetMeasure{{Key: "completed_sessions", Operation: "count", SourceAlias: "facts"}, {Key: "commission", Operation: "sum", Field: field("amount")}, {Key: "renewal_rate", Operation: "avg", Field: field("ratio_value")}}
	trainer.Dataset.Sort = []reportmodel.ReportDatasetSort{{Key: "commission", Direction: "desc"}}

	acquisition := base("gym_acquisition_funnel", "acquisition")
	acquisition.Dataset.Dimensions = []reportmodel.ReportDatasetDimension{{Key: "channel", Field: *field("category")}}
	acquisition.Dataset.Measures = []reportmodel.ReportDatasetMeasure{{Key: "events", Operation: "count", SourceAlias: "facts"}, {Key: "coupon_or_referral_rate", Operation: "avg", Field: field("ratio_value")}}
	acquisition.Dataset.Analyses = []reportmodel.ReportDatasetAnalysis{{Key: "acquisition", Type: "funnel", EntityField: *field("entity_id"), EventField: field("event_key"), TimeField: *field("occurred_at"), Stages: []reportmodel.ReportDatasetFunnelStage{{Key: "lead", Values: []any{"lead"}}, {Key: "trial", Values: []any{"trial"}}, {Key: "converted", Values: []any{"converted"}}}, WindowSeconds: 2592000}}
	return []reportmodel.ReportSchema{revenue, retention, attendance, equipment, trainer, acquisition}
}

func gymAnalyticsP7Field(key string) *reportmodel.ReportDatasetField {
	return &reportmodel.ReportDatasetField{SourceAlias: "facts", FieldKey: key}
}

func gymAnalyticsP7ReportKeys() []string {
	return []string{"gym_revenue", "gym_member_retention", "gym_attendance", "gym_equipment", "gym_trainer_performance", "gym_acquisition_funnel"}
}

func (environment *gymAnalyticsP7Environment) seedAcceptanceFacts(t *testing.T) {
	t.Helper()
	records := recordpersistence.NewRecordStore(environment.store)
	facts := []recordmodel.Record{
		gymAnalyticsP7Fact("rev-1", "revenue", "order-1", "paid", "2026-06-01T09:00:00Z", "advisor-1", "cash", "100.10", "1", "", ""),
		gymAnalyticsP7Fact("rev-2", "revenue", "order-2", "paid", "2026-06-01T10:00:00Z", "advisor-2", "online", "200.20", "1", "", ""),
		gymAnalyticsP7Fact("rev-3", "revenue", "order-3", "refund", "2026-06-02T10:00:00Z", "advisor-1", "online", "-20.05", "1", "", ""),
		gymAnalyticsP7Fact("mem-1", "membership", "member-1", "new", "2026-01-02T00:00:00Z", "advisor-1", "", "0", "1", "", ""),
		gymAnalyticsP7Fact("mem-2", "membership", "member-1", "active", "2026-02-02T00:00:00Z", "advisor-1", "", "0", "1", "", ""),
		gymAnalyticsP7Fact("mem-3", "membership", "member-2", "new", "2026-01-03T00:00:00Z", "advisor-2", "", "0", "1", "", ""),
		gymAnalyticsP7Fact("mem-4", "membership", "member-3", "churned", "2026-02-03T00:00:00Z", "advisor-2", "", "0", "0", "", ""),
		gymAnalyticsP7Fact("att-1", "attendance", "member-1", "attended", "2026-06-01T09:10:00Z", "trainer-1", "group", "0", "1", "", ""),
		gymAnalyticsP7Fact("att-2", "attendance", "member-2", "absent", "2026-06-01T09:20:00Z", "trainer-1", "group", "0", "0", "", ""),
		gymAnalyticsP7Fact("att-3", "attendance", "member-3", "completed", "2026-06-01T10:10:00Z", "trainer-2", "private", "0", "1", "", ""),
		gymAnalyticsP7Fact("eq-1", "equipment", "equipment-1", "repaired", "2026-06-01T12:00:00Z", "operator-1", "cardio", "0", "0.8", "2026-06-01T10:00:00Z", "2026-06-01T12:00:00Z"),
		gymAnalyticsP7Fact("eq-2", "equipment", "equipment-2", "repaired", "2026-06-01T13:00:00Z", "operator-1", "cardio", "0", "0.6", "2026-06-01T12:00:00Z", "2026-06-01T13:00:00Z"),
		gymAnalyticsP7Fact("train-1", "training", "session-1", "completed", "2026-06-01T11:00:00Z", "trainer-1", "private", "30.00", "1", "", ""),
		gymAnalyticsP7Fact("train-2", "training", "session-2", "completed", "2026-06-02T11:00:00Z", "trainer-1", "private", "40.00", "0", "", ""),
		gymAnalyticsP7Fact("train-3", "training", "session-3", "completed", "2026-06-02T12:00:00Z", "trainer-2", "private", "50.00", "1", "", ""),
		gymAnalyticsP7Fact("lead-1", "acquisition", "prospect-1", "lead", "2026-06-01T00:00:00Z", "advisor-1", "referral", "0", "1", "", ""),
		gymAnalyticsP7Fact("lead-2", "acquisition", "prospect-1", "trial", "2026-06-02T00:00:00Z", "advisor-1", "referral", "0", "1", "", ""),
		gymAnalyticsP7Fact("lead-3", "acquisition", "prospect-1", "converted", "2026-06-03T00:00:00Z", "advisor-1", "referral", "0", "1", "", ""),
		gymAnalyticsP7Fact("lead-4", "acquisition", "prospect-2", "lead", "2026-06-01T00:00:00Z", "advisor-2", "coupon", "0", "1", "", ""),
		gymAnalyticsP7Fact("lead-5", "acquisition", "prospect-2", "trial", "2026-06-04T00:00:00Z", "advisor-2", "coupon", "0", "0", "", ""),
	}
	for _, fact := range facts {
		if err := records.InsertRecord(t.Context(), "default", environment.object, fact); err != nil {
			t.Fatalf("insert %s: %v", fact.ID, err)
		}
	}
}

func gymAnalyticsP7Fact(id, metricType, entityID, eventKey, occurredAt, owner, category, amount, ratioValue, startedAt, endedAt string) recordmodel.Record {
	data := map[string]any{"metric_type": metricType, "entity_id": entityID, "event_key": eventKey, "occurred_at": occurredAt, "owner": owner, "category": category, "amount": amount, "ratio_value": ratioValue}
	if startedAt != "" {
		data["started_at"] = startedAt
	}
	if endedAt != "" {
		data["ended_at"] = endedAt
	}
	return recordmodel.Record{ID: id, CreatedAt: occurredAt, UpdatedAt: occurredAt, Data: data}
}

func (environment *gymAnalyticsP7Environment) assertReportMatchesOwnedDetail(t *testing.T, reportKey, roleKey, metricType string) {
	t.Helper()
	principal := environment.principals[roleKey]
	page, err := environment.service.Applications().Records.ListRecords(t.Context(), environment.object.Key, recordmodel.RecordListQuery{Page: 1, PageSize: 200}, principal)
	if err != nil {
		t.Fatal(err)
	}
	detailFacts := 0
	for _, item := range page.Items {
		if fmt.Sprint(item.Data["metric_type"]) == metricType {
			detailFacts++
		}
	}
	summary, err := environment.service.Applications().ReportQueries.Summary(t.Context(), reportKey, principal)
	if err != nil || summary.SourceRowCount != detailFacts {
		t.Fatalf("RLS parity report=%s source_rows=%d detail_rows=%d err=%v summary=%#v", reportKey, summary.SourceRowCount, detailFacts, err, summary)
	}
}

func assertGymRevenueDashboard(t *testing.T, summary reportmodel.ReportSummary) {
	t.Helper()
	if len(summary.Rows) != 3 || gymAnalyticsP7MeasureTotal(summary.Rows, "revenue") != "280.25" || gymAnalyticsP7MeasureTotal(summary.Rows, "orders") != "3" {
		t.Fatalf("revenue dashboard=%#v", summary)
	}
}

func assertGymRetentionDashboard(t *testing.T, summary reportmodel.ReportSummary) {
	t.Helper()
	if len(summary.Analyses) != 1 || len(summary.Analyses[0].Rows) < 2 {
		t.Fatalf("retention dashboard=%#v", summary)
	}
	foundHalf := false
	for _, row := range summary.Analyses[0].Rows {
		foundHalf = foundHalf || row.Measures["retention_rate"] == "0.500000"
	}
	if !foundHalf {
		t.Fatalf("retention rate missing: %#v", summary.Analyses[0].Rows)
	}
}

func assertGymAttendanceDashboard(t *testing.T, summary reportmodel.ReportSummary) {
	t.Helper()
	if gymAnalyticsP7MeasureTotal(summary.Rows, "visits") != "3" {
		t.Fatalf("attendance dashboard=%#v", summary)
	}
}

func assertGymEquipmentDashboard(t *testing.T, summary reportmodel.ReportSummary) {
	t.Helper()
	if len(summary.Rows) != 1 || summary.Rows[0].Measures["events"] != "2" || summary.Rows[0].Measures["repair_seconds"] != "10800" || summary.Rows[0].Measures["average_repair_seconds"] != "5400.000000" {
		t.Fatalf("equipment dashboard=%#v", summary)
	}
}

func assertGymTrainerDashboard(t *testing.T, summary reportmodel.ReportSummary) {
	t.Helper()
	if gymAnalyticsP7MeasureTotal(summary.Rows, "completed_sessions") != "3" || gymAnalyticsP7MeasureTotal(summary.Rows, "commission") != "120.00" {
		t.Fatalf("trainer dashboard=%#v", summary)
	}
}

func assertGymAcquisitionDashboard(t *testing.T, summary reportmodel.ReportSummary) {
	t.Helper()
	if len(summary.Analyses) != 1 || len(summary.Analyses[0].Rows) != 3 || summary.Analyses[0].Rows[2].Measures["entities"] != "1" || summary.Analyses[0].Rows[2].Measures["conversion_rate"] != "0.500000" {
		t.Fatalf("acquisition dashboard=%#v", summary)
	}
}

func gymAnalyticsP7MeasureTotal(rows []reportmodel.ReportResultRow, key string) string {
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		values = append(values, row.Measures[key])
	}
	sort.Strings(values)
	// Exact-decimal addition is exercised by the Report owner itself. These
	// acceptance totals remain intentionally tiny and deterministic.
	switch key {
	case "orders", "visits", "completed_sessions":
		total := 0
		for _, value := range values {
			var parsed int
			_, _ = fmt.Sscan(value, &parsed)
			total += parsed
		}
		return fmt.Sprint(total)
	case "revenue", "commission":
		cents := int64(0)
		for _, value := range values {
			var whole, fractional int64
			var sign int64 = 1
			if len(value) > 0 && value[0] == '-' {
				sign, value = -1, value[1:]
			}
			_, _ = fmt.Sscanf(value, "%d.%d", &whole, &fractional)
			cents += sign * (whole*100 + fractional)
		}
		return fmt.Sprintf("%d.%02d", cents/100, cents%100)
	default:
		return ""
	}
}

func TestGymDashboardTargetVolumeP95IsUnderThreeSeconds(t *testing.T) {
	environment := newGymAnalyticsP7Environment(t)
	environment.seedTargetVolume(t, 5000)
	manager := environment.principals["manager"]

	// Realtime cards prove the indexed source path; the other cards prove the
	// configured T+1 materialization path and expose freshness on every read.
	for _, reportKey := range gymAnalyticsP7ReportKeys()[2:] {
		if _, err := environment.service.Applications().ReportSnapshots.RefreshSnapshot(t.Context(), reportKey, "performance-"+reportKey, manager); err != nil {
			t.Fatalf("refresh %s: %v", reportKey, err)
		}
	}
	modes := map[string]string{"gym_revenue": "realtime", "gym_member_retention": "realtime"}
	durations := make([]time.Duration, 0, len(gymAnalyticsP7ReportKeys())*20)
	for _, reportKey := range gymAnalyticsP7ReportKeys() {
		mode := modes[reportKey]
		if mode == "" {
			mode = "snapshot"
		}
		for attempt := 0; attempt < 20; attempt++ {
			started := time.Now()
			summary, err := environment.service.Applications().ReportQueries.SummaryMode(t.Context(), reportKey, mode, manager)
			elapsed := time.Since(started)
			if err != nil || summary.SourceRowCount == 0 || mode == "snapshot" && (summary.Snapshot == nil || summary.Snapshot.Stale) {
				t.Fatalf("performance report=%s mode=%s summary=%#v err=%v", reportKey, mode, summary, err)
			}
			durations = append(durations, elapsed)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(len(durations)*95+99)/100-1]
	t.Logf("30,000-fact dashboard samples=%d p95=%s", len(durations), p95)
	if p95 > 3*time.Second {
		t.Fatalf("30,000-fact dashboard P95=%s exceeds 3s; samples=%v", p95, durations)
	}
}

func (environment *gymAnalyticsP7Environment) seedTargetVolume(t *testing.T, factsPerDashboard int) {
	t.Helper()
	tx, err := environment.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(t.Context(), `INSERT INTO "gym_metric_fact" ("workspace_id","id","created_at","updated_at","metric_type","entity_id","event_key","occurred_at","owner","category","amount","ratio_value","started_at","ended_at") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	defer statement.Close()
	metricTypes := []string{"revenue", "membership", "attendance", "equipment", "training", "acquisition"}
	for metricIndex, metricType := range metricTypes {
		for index := 0; index < factsPerDashboard; index++ {
			occurred := time.Date(2025+index%2, time.Month(index%12+1), index%28+1, index%24, 0, 0, 0, time.UTC)
			event := "active"
			switch metricType {
			case "acquisition":
				event = []string{"lead", "trial", "converted"}[index%3]
			case "membership":
				event = []string{"new", "active", "churned"}[index%3]
			case "attendance", "training", "equipment":
				event = "completed"
			case "revenue":
				event = "paid"
			}
			owner := []string{"trainer-1", "trainer-2", "advisor-1", "advisor-2"}[index%4]
			started := occurred.Add(-time.Duration(index%1800) * time.Second).Format(time.RFC3339)
			if _, err := statement.ExecContext(t.Context(), "default", fmt.Sprintf("perf-%d-%d", metricIndex, index), occurred.Format(time.RFC3339), occurred.Format(time.RFC3339), metricType, fmt.Sprintf("entity-%d", index/3), event, occurred.Format(time.RFC3339), owner, []string{"cash", "online", "cardio", "private"}[index%4], fmt.Sprintf("%d.%02d", index%100+1, index%100), "0.500000", started, occurred.Format(time.RFC3339)); err != nil {
				_ = tx.Rollback()
				t.Fatalf("seed target volume: %v", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
