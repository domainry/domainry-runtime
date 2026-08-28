package export

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func Rows(summary reportmodel.ReportSummary, analysisKey string) ([]reportmodel.ReportResultRow, error) {
	if strings.TrimSpace(analysisKey) == "" {
		return summary.Rows, nil
	}
	for _, analysis := range summary.Analyses {
		if analysis.Key == analysisKey {
			return analysis.Rows, nil
		}
	}
	return nil, exportScopeError("backend.report.export_analysis_not_allowed")
}

func EncodeCSV(rows []reportmodel.ReportResultRow, projection []string, maskedDimensions map[string]bool) ([]byte, error) {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	_ = writer.Write(projection)
	for _, source := range rows {
		row := make([]string, 0, len(projection))
		for _, key := range projection {
			value, ok := source.Dimensions[key]
			if !ok {
				value = source.Measures[key]
			}
			if maskedDimensions[key] && value != "" {
				value = "******"
			}
			row = append(row, value)
		}
		_ = writer.Write(row)
	}
	writer.Flush()
	return output.Bytes(), writer.Error()
}

func ApplyWatermark(content []byte, watermark, expiresAt string) ([]byte, error) {
	rows, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read report export csv: %w", err)
	}
	if len(rows) == 0 {
		return content, nil
	}
	rows[0] = append(rows[0], "export_watermark", "download_expires_at")
	for index := 1; index < len(rows); index++ {
		rows[index] = append(rows[index], watermark, expiresAt)
	}
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.WriteAll(rows)
	return output.Bytes(), writer.Error()
}
