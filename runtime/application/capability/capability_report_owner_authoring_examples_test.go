package capability

import (
	"encoding/json"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestReportAuthoringExamplesExecuteRuntimeReportValidator(t *testing.T) {
	capability := reportcontract.ReportAuthoringDomain().Capabilities[0]
	snapshot := metadatamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
	}
	for _, example := range capability.Examples {
		payload, err := json.Marshal(example.Value["payload"])
		if err != nil {
			t.Fatal(err)
		}
		var report reportmodel.ReportSchema
		if err := json.Unmarshal(payload, &report); err != nil {
			t.Fatal(err)
		}
		issues := metadatavalidation.MetadataValidateReportDefinitionContract(t.Context(), snapshot, report)
		if len(example.ExpectedErrorCodes) == 0 {
			if len(issues) != 0 {
				t.Fatalf("example=%s value=%#v issues=%#v", example.Name, example.Value, issues)
			}
			continue
		}
		found := false
		for _, issue := range issues {
			if issue.ErrorCode == example.ExpectedErrorCodes[0] {
				found = true
			}
		}
		if !found {
			t.Fatalf("example=%s issues=%#v want=%s", example.Name, issues, example.ExpectedErrorCodes[0])
		}
	}
}
