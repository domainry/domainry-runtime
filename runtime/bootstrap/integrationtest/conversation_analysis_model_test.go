package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agent "github.com/domainry/domainry-agent-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

// Deterministic SDK decisions over actual owner responses, not a model-vendor
// or analysis-engine fixture. Real Report computes every returned value.
type analysisToolsModel struct{ businessWebModel }

func (analysisToolsModel) ConversationModelIdentity() agent.ConversationModelIdentity {
	return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "analysis-tools", Fingerprint: "analysis-tools-v1"}
}

func (analysisToolsModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, emit func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	answer := func(text string) (agent.ConversationStepResult, error) {
		if err := emit(agent.ConversationModelEvent{Type: "text.delta", Delta: text}); err != nil {
			return agent.ConversationStepResult{}, err
		}
		return agent.ConversationStepResult{Message: agent.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
	}
	available := false
	for _, d := range in.Tools {
		available = available || d.Key == toolsdk.AnalysisRunToolKey
	}
	if !available {
		return answer("当前没有可用分析工具，未执行分析。")
	}
	call := func(id string, args any) (agent.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agent.ConversationStepResult{Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: toolsdk.AnalysisRunToolKey, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	results := map[string]model.AnalysisResult{}
	var dataset *model.AnalysisDataset
	catalogSeen := false
	limitSeen := false
	for _, message := range in.Messages {
		if message.Role == "user" {
			results = map[string]model.AnalysisResult{}
			dataset = nil
			catalogSeen = false
			limitSeen = false
			continue
		}
		if message.Role != "tool" {
			continue
		}
		var result toolsdk.Result
		var out struct {
			Catalog *model.AnalysisCatalog `json:"catalog"`
			Result  *model.AnalysisResult  `json:"result"`
		}
		if json.Unmarshal([]byte(message.Content), &result) == nil && result.Status == "failed" && result.ErrorCode == "backend.report.analysis.result_limit_exceeded" {
			if !strings.Contains(string(result.Content), "Never substitute sampled query pages") {
				return agent.ConversationStepResult{}, fmt.Errorf("limit recovery instruction missing")
			}
			limitSeen = true
			continue
		}
		if result.Status != "completed" || json.Unmarshal(result.Content, &out) != nil {
			return agent.ConversationStepResult{}, fmt.Errorf("analysis did not return completed owner data: %s", message.Content)
		}
		if out.Catalog != nil {
			catalogSeen = true
			for _, item := range out.Catalog.Datasets {
				if item.Key == "customer" {
					copy := item
					dataset = &copy
				}
			}
		}
		if out.Result != nil {
			if out.Result.Source.Proof == "" || !out.Result.Source.Complete || len(out.Result.Methods) == 0 {
				return agent.ConversationStepResult{}, fmt.Errorf("analysis lost completeness, proof or methods")
			}
			results[out.Result.Spec.Mode] = *out.Result
		}
	}
	if dataset == nil {
		if catalogSeen {
			return answer("当前没有可用客户数据集，未执行分析。")
		}
		return call("analysis-catalog", map[string]any{"operation": "catalog", "dataset_key": "customer"})
	}
	columns := map[string]bool{}
	for _, column := range dataset.Columns {
		columns[column.Key] = true
	}
	if !columns["balance"] || !columns["name"] || !columns["created_at"] {
		return answer("当前分析目录缺少获准字段，未执行分析。")
	}
	filter := func(name string) []model.AnalysisFilter {
		return []model.AnalysisFilter{{Field: "name", Operator: "eq", Values: []any{name}}}
	}
	for _, mode := range []string{"aggregate", "compare", "trend", "table"} {
		if _, ok := results[mode]; ok {
			continue
		}
		spec := model.AnalysisRequest{DatasetKey: dataset.Key, Mode: mode, Measures: []model.AnalysisMeasure{{Key: "total", Field: "balance", Function: "sum"}, {Key: "mean", Field: "balance", Function: "avg"}, {Key: "rows", Function: "count"}}, MaxRows: 100}
		switch mode {
		case "aggregate":
			if !limitSeen {
				spec.GroupBy = []string{"name"}
				spec.MaxRows = 1
				return call("analysis-output-limit", map[string]any{"operation": "run", "spec": spec})
			}
		case "compare":
			spec.Comparison = &model.AnalysisComparison{Baseline: filter("Beta"), Current: filter("Gamma")}
		case "trend":
			spec.Time = &model.AnalysisTimeBucket{Field: "created_at", Grain: "day", TimeZone: "UTC"}
		case "table":
			spec.Measures = nil
			spec.Select = []string{"name", "balance"}
			spec.Calculations = []model.AnalysisCalculation{{Key: "doubled", Scale: 2, Expression: model.AnalysisExpression{Operator: "multiply", Arguments: []model.AnalysisExpression{{Reference: "balance"}, {Decimal: "2"}}}}}
			spec.AnomalyRules = []model.AnalysisAnomalyRule{{Key: "large", Column: "doubled", Operator: "ge", Values: []string{"40"}}}
		}
		return call("analysis-"+mode, map[string]any{"operation": "run", "spec": spec})
	}
	cell := func(r model.AnalysisRow, k string) string {
		if r.Values[k] == nil {
			return "NULL"
		}
		return *r.Values[k]
	}
	a, c, tr, table := results["aggregate"], results["compare"], results["trend"], results["table"]
	if len(a.Rows) != 1 || len(c.Rows) != 1 || len(tr.Rows) == 0 || len(table.Rows) == 0 {
		return agent.ConversationStepResult{}, fmt.Errorf("actual analysis rows missing")
	}
	names := []string{}
	flagged := 0
	for _, row := range table.Rows {
		names = append(names, cell(row, "name")+"="+cell(row, "doubled"))
		flagged += len(row.Anomalies)
	}
	return answer(fmt.Sprintf("分析已完成：%s 条授权记录，总计 %s，均值 %s（非空 %s 条）。对比基准 %s、当前 %s、差值 %s；趋势按 UTC 日历日分成 %d 个有数据的时段。表格翻倍计算：%s；%d 条触发翻倍值至少 40 的规则。来源：%s；原始单位未指定。四种模式均返回完整结果，筛选条件、非空计数、计算方法与来源版本保留在工具记录中。", a.Source.InputCounts["dataset"], cell(a.Rows[0], "total"), cell(a.Rows[0], "mean"), a.Rows[0].NonNullCounts["mean"], cell(c.Rows[0], "baseline_total"), cell(c.Rows[0], "current_total"), cell(c.Rows[0], "delta_total"), len(tr.Rows), strings.Join(names, "、"), flagged, a.Source.DatasetKey))
}
