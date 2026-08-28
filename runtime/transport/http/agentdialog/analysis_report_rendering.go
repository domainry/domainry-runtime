package agentdialog

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"unicode/utf8"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func agentAnalysisValidationHTML(intent, queryRef, reportKey, scopeNote string) string {
	return `<article data-card-type="message_card" class="agent-card agent-card-message">` + `<h3>` + html.EscapeString(agentAnalysisTitle(intent, "Analysis query")) + `</h3>` + `<p>The query was validated by the generated backend. Execution requires the permission-aware SQL executor or a metric spec that can be served through generated scoped APIs.</p>` + agentAnalysisReportCenterHTML(reportKey) + agentAnalysisReportGovernanceHTML(queryRef, reportKey) + `<p data-scope-note="analysis_gateway">` + html.EscapeString(scopeNote) + `</p>` + `<a data-reference="` + html.EscapeString(queryRef) + `">` + html.EscapeString(queryRef) + `</a>` + `</article>`
}

func agentAnalysisReportHTML(intent, queryRef, objectKey, reportKey string, rows []recordmodel.Record, scopeNote string) string {
	var builder strings.Builder
	builder.WriteString(`<article data-card-type="report_card" class="agent-card agent-card-report"`)
	if strings.TrimSpace(reportKey) != "" {
		builder.WriteString(` data-report-center-ref="` + html.EscapeString(strings.TrimSpace(reportKey)) + `"`)
	}
	builder.WriteString(`><h3>` + html.EscapeString(agentAnalysisTitle(intent, "Analysis result")) + `</h3>`)
	builder.WriteString(agentAnalysisReportCenterHTML(reportKey))
	builder.WriteString(agentAnalysisReportGovernanceHTML(queryRef, reportKey))
	builder.WriteString(`<div data-chart-type="table" data-chart-title="` + html.EscapeString(agentAnalysisTitle(intent, objectKey)) + `">`)
	if len(rows) == 0 {
		builder.WriteString(`<div data-chart-item data-label="Rows" data-value="0"></div>`)
	} else {
		for index, row := range rows {
			if index >= 10 {
				break
			}
			builder.WriteString(`<div data-chart-item data-label="` + html.EscapeString(agentAnalysisRowLabel(row, index)) + `" data-value="` + html.EscapeString(agentAnalysisRowValue(row)) + `"></div>`)
		}
	}
	builder.WriteString(`</div><p data-scope-note="analysis_gateway">` + html.EscapeString(scopeNote) + `</p><a data-reference="` + html.EscapeString(queryRef) + `">` + html.EscapeString(queryRef) + `</a></article>`)
	return builder.String()
}

func agentAnalysisReportCenterHTML(reportKey string) string {
	reportKey = strings.TrimSpace(reportKey)
	if reportKey == "" {
		return ""
	}
	return `<a data-report-center-ref="` + html.EscapeString(reportKey) + `" data-reference="report:` + html.EscapeString(reportKey) + `">Open report center reference</a>`
}

func agentAnalysisReportGovernanceHTML(queryRef, reportKey string) string {
	if strings.TrimSpace(reportKey) == "" {
		return ""
	}
	escapedQuery := html.EscapeString(queryRef)
	return `<div data-evidence-kind="report_governance"><a data-reference="report_query_run:` + escapedQuery + `">Query run</a><a data-reference="report_export_audit:` + escapedQuery + `">Export audit handoff</a><a data-reference="download_task:` + escapedQuery + `">Download task handoff</a></div>`
}

func agentAnalysisTitle(intent, fallback string) string {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return fallback
	}
	if len(intent) > 80 {
		return agentAnalysisTruncateUTF8(intent, 80)
	}
	return intent
}

func agentAnalysisRowLabel(row recordmodel.Record, index int) string {
	if row.ID != "" {
		return row.ID
	}
	return fmt.Sprintf("row_%d", index+1)
}

func agentAnalysisRowValue(row recordmodel.Record) string {
	keys := make([]string, 0, len(row.Data))
	for key := range row.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, 3)
	for _, key := range keys {
		if len(values) >= 3 {
			break
		}
		value := strings.TrimSpace(fmt.Sprint(row.Data[key]))
		if value == "" {
			continue
		}
		if len(value) > 60 {
			value = agentAnalysisTruncateUTF8(value, 60)
		}
		values = append(values, key+"="+value)
	}
	if len(values) == 0 {
		return "record"
	}
	return strings.Join(values, ", ")
}

func agentAnalysisTruncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
