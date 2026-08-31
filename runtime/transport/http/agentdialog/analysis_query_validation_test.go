package agentdialog

import (
	"strings"
	"testing"
	"unicode/utf8"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestAgentAnalysisSQLRejectReason(t *testing.T) {
	tests := []struct {
		sql  string
		want string
	}{
		{sql: ""},
		{sql: "SELECT * FROM customer"},
		{sql: "WITH active AS (SELECT 1) SELECT * FROM active"},
		{sql: "EXPLAIN SELECT * FROM customer"},
		{sql: "SELECT 1;"},
		{sql: "SELECT 1; SELECT 2", want: "agent_analysis.sql_multi_statement_denied"},
		{sql: "SELECT 1;;", want: "agent_analysis.sql_multi_statement_denied"},
		{sql: "UPDATE customer SET name='x'", want: "agent_analysis.sql_readonly_required"},
		{sql: "SELECT * INTO customer_snapshot FROM customer", want: "agent_analysis.sql_readonly_required"},
		{sql: "SELECT * FROM customer INTO OUTFILE '/tmp/x'", want: "agent_analysis.sql_readonly_required"},
		{sql: "SELECT * FROM customer INTO DUMPFILE '/tmp/x'", want: "agent_analysis.sql_readonly_required"},
		{sql: "SELECT load_file('/tmp/x')", want: "agent_analysis.sql_readonly_required"},
		{sql: "WITH changed AS (DELETE FROM customer RETURNING *) SELECT * FROM changed", want: "agent_analysis.sql_readonly_required"},
	}
	for _, test := range tests {
		if got := agentAnalysisSQLRejectReason(test.sql); got != test.want {
			t.Errorf("SQL %q reason=%q want=%q", test.sql, got, test.want)
		}
	}
}

func TestAgentAnalysisValidationHelpers(t *testing.T) {
	spec := map[string]any{"object_key": " customer ", "report_key": " report-1 ", "filters": map[string]any{"status": "active"}}
	if got := agentAnalysisSpecString(spec, "object_key"); got != "customer" || agentAnalysisSpecString(nil, "object_key") != "" {
		t.Fatalf("spec string=%q", got)
	}
	if filters := agentAnalysisFilters(spec); filters["status"] != "active" || agentAnalysisFilters(nil) != nil || agentAnalysisFilters(map[string]any{"filters": "invalid"}) != nil {
		t.Fatalf("filters=%#v", filters)
	}
	objects := []definitionmodel.ObjectSchema{{Key: "customer"}}
	reports := []reportmodel.ReportSchema{{Key: "report-1"}}
	if !agentAnalysisObjectVisible(objects, "customer") || agentAnalysisObjectVisible(objects, "order") || !agentAnalysisReportVisible(reports, "report-1") || agentAnalysisReportVisible(reports, "missing") {
		t.Fatal("object/report visibility mismatch")
	}
	first := agentAnalysisQueryRef("intent", "select 1", spec, "workspace", "admin")
	second := agentAnalysisQueryRef("intent", "select 1", spec, "workspace", "admin")
	changed := agentAnalysisQueryRef("intent", "select 2", spec, "workspace", "admin")
	if first != second || first == changed || !strings.HasPrefix(first, "agent_query:") || len(first) != len("agent_query:")+16 {
		t.Fatalf("query refs first=%q second=%q changed=%q", first, second, changed)
	}
}

func TestAgentAnalysisReportMetadata(t *testing.T) {
	rendered := agentAnalysisRenderedReport(" report_card ", "<article></article>", "query-1", " report-1 ")
	if rendered["format"] != "html_fragment" || rendered["card_type"] != " report_card " || rendered["report_center_ref"] != "report-1" || rendered["canonical"] != true {
		t.Fatalf("rendered report=%#v", rendered)
	}
	withoutReport := agentAnalysisReportProvenance("query-1", "", " customer ", 2, 5, true, "audit-1")
	if withoutReport["source_object_key"] != "customer" || withoutReport["row_count"] != 2 || withoutReport["truncated"] != true || withoutReport["report_center_ref"] != nil {
		t.Fatalf("provenance without report=%#v", withoutReport)
	}
	withReport := agentAnalysisReportProvenance("query-1", " report-1 ", "customer", 1, 1, false, "audit-1")
	if withReport["report_center_ref"] != "report-1" || withReport["report_grid_ref"] != "report_grid:report-1" {
		t.Fatalf("provenance with report=%#v", withReport)
	}
}

func TestAgentAnalysisReportRenderingEscapesBoundsAndLabelsRows(t *testing.T) {
	validation := agentAnalysisValidationHTML("<Intent>", `query"1`, `report"1`, "scope <safe>")
	for _, fragment := range []string{"&lt;Intent&gt;", `query&#34;1`, `report&#34;1`, "scope &lt;safe&gt;", "report_query_run:"} {
		if !strings.Contains(validation, fragment) {
			t.Fatalf("validation HTML missing %q: %s", fragment, validation)
		}
	}
	if agentAnalysisReportCenterHTML(" ") != "" || agentAnalysisReportGovernanceHTML("query", "") != "" {
		t.Fatal("empty report key generated report links")
	}

	rows := []recordmodel.Record{{ID: "row<1>", Data: map[string]any{"z": "last", "a": "first", "empty": "", "m": "middle", "x": "ignored"}}, {Data: map[string]any{}}}
	for index := 0; index < 10; index++ {
		rows = append(rows, recordmodel.Record{ID: "extra-" + string(rune('a'+index)), Data: map[string]any{"value": index}})
	}
	report := agentAnalysisReportHTML("", "query-1", "customer", "report-1", rows, "scoped")
	if !strings.Contains(report, "Analysis result") || !strings.Contains(report, "row&lt;1&gt;") || !strings.Contains(report, `data-label="row_2"`) || !strings.Contains(report, `data-value="record"`) || strings.Contains(report, "extra-j") {
		t.Fatalf("report HTML=%s", report)
	}
	if value := agentAnalysisRowValue(rows[0]); value != "a=first, m=middle, x=ignored" {
		t.Fatalf("sorted row value=%q", value)
	}
	if agentAnalysisRowLabel(recordmodel.Record{ID: "explicit"}, 4) != "explicit" || agentAnalysisRowLabel(recordmodel.Record{}, 4) != "row_5" {
		t.Fatal("row label fallback mismatch")
	}
	empty := agentAnalysisReportHTML("Empty", "query-empty", "customer", "", nil, "scoped")
	if !strings.Contains(empty, `data-label="Rows" data-value="0"`) || strings.Contains(empty, "data-report-center-ref") {
		t.Fatalf("empty report HTML=%s", empty)
	}
}

func TestAgentAnalysisRenderingTruncationPreservesUTF8(t *testing.T) {
	title := agentAnalysisTitle(strings.Repeat("界", 40), "fallback")
	if len(title) > 80 || !utf8.ValidString(title) {
		t.Fatalf("title bytes=%d validUTF8=%v", len(title), utf8.ValidString(title))
	}
	value := agentAnalysisRowValue(recordmodel.Record{Data: map[string]any{"label": strings.Repeat("界", 30)}})
	if !utf8.ValidString(value) {
		t.Fatalf("row value is invalid UTF-8: %q", value)
	}
	if agentAnalysisTitle(" ", "fallback") != "fallback" || agentAnalysisTitle(" short ", "fallback") != "short" {
		t.Fatal("title fallback/trim mismatch")
	}
	if agentAnalysisTruncateUTF8("short", 80) != "short" {
		t.Fatal("short UTF-8 value was changed")
	}
}
