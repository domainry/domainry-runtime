package agentdialog

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func agentAnalysisRenderedReport(cardType, htmlFragment, queryRef, reportKey string) map[string]any {
	return map[string]any{"format": "html_fragment", "card_type": cardType, "html_fragment": htmlFragment, "query_ref": queryRef, "report_center_ref": strings.TrimSpace(reportKey), "rendering_owner": "generated_backend", "canonical": true}
}

func agentAnalysisReportProvenance(queryRef, reportKey, objectKey string, rowCount, total int, truncated bool, auditEventKey string) map[string]any {
	reportKey = strings.TrimSpace(reportKey)
	provenance := map[string]any{"query_ref": queryRef, "source_object_key": strings.TrimSpace(objectKey), "row_count": rowCount, "total": total, "truncated": truncated, "audit_event_key": auditEventKey, "governance": "server_principal_scoped", "rendering_owner": "generated_backend", "export_audit_required_for_export": true, "export_audit_handoff": "report_center"}
	if reportKey != "" {
		provenance["report_center_ref"], provenance["report_grid_ref"] = reportKey, "report_grid:"+reportKey
	}
	return provenance
}

func agentAnalysisSQLRejectReason(sqlText string) string {
	lower := strings.ToLower(strings.TrimSpace(sqlText))
	if lower == "" {
		return ""
	}
	if strings.Count(lower, ";") > 1 || strings.Contains(strings.TrimSuffix(lower, ";"), ";") {
		return "agent_analysis.sql_multi_statement_denied"
	}
	if !strings.HasPrefix(lower, "select ") && !strings.HasPrefix(lower, "with ") && !strings.HasPrefix(lower, "explain ") {
		return "agent_analysis.sql_readonly_required"
	}
	tokens := strings.FieldsFunc(lower, func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '_'
	})
	for _, token := range tokens {
		switch token {
		case "insert", "update", "delete", "merge", "upsert", "replace", "create", "alter", "drop", "truncate", "grant", "revoke", "call", "load", "load_file":
			return "agent_analysis.sql_readonly_required"
		}
	}
	for _, token := range tokens {
		if token == "into" {
			return "agent_analysis.sql_readonly_required"
		}
	}
	return ""
}

func agentAnalysisSpecString(spec map[string]any, key string) string {
	if spec == nil {
		return ""
	}
	text, _ := spec[key].(string)
	return strings.TrimSpace(text)
}

func agentAnalysisFilters(spec map[string]any) map[string]any {
	if spec == nil {
		return nil
	}
	filters, _ := spec["filters"].(map[string]any)
	return filters
}

func agentAnalysisObjectVisible(objects []definitionmodel.ObjectSchema, key string) bool {
	for _, object := range objects {
		if object.Key == key {
			return true
		}
	}
	return false
}

func agentAnalysisReportVisible(reports []reportmodel.ReportSchema, key string) bool {
	for _, report := range reports {
		if report.Key == key {
			return true
		}
	}
	return false
}

func agentAnalysisQueryRef(intent, sqlText string, spec map[string]any, workspaceID, role string) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{intent, sqlText, agentAnalysisSpecString(spec, "object_key"), agentAnalysisSpecString(spec, "report_key"), workspaceID, role}, "|")))
	return "agent_query:" + hex.EncodeToString(hash[:])[:16]
}
