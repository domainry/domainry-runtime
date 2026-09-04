package validation

import (
	"fmt"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func testObjectSQLReport(key, objectKey string) reportmodel.ReportSchema {
	return reportmodel.ReportSchema{Key: key, ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL:           fmt.Sprintf("SELECT source.id AS id FROM `%s` source ORDER BY source.id LIMIT 100", objectKey),
		SourceObjects: []string{objectKey},
		ResultSchema:  []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
}
