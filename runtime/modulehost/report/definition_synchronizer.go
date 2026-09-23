package reportmodulehost

import (
	"context"
	"fmt"
	"strings"

	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportpersistence "github.com/domainry/domainry-report-sdk/persistence"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

// SynchronizeDefinitions sends code-owned Report definitions directly to the
// Report repository. No JSON model or Runtime envelope transports behavior.
func SynchronizeDefinitions(ctx context.Context, binding reportsdk.Binding, sourceID, revision string, reports []runtimeext.ReportDefinition) error {
	repository := binding.Definitions()
	if repository == nil {
		return fmt.Errorf("Report Binding returned no definition repository")
	}
	definitions := make([]reportmodel.ReportDefinitionSchema, 0, len(reports))
	for _, definition := range reports {
		definitions = append(definitions, reportmodel.ReportDefinitionSchema{
			Report:                 definition.Report,
			OperationStateExamples: append([]reportmodel.ReportOperationStateExampleSchema(nil), definition.OperationStateExamples...),
			SensitiveFieldPolicies: append([]reportmodel.ReportSensitiveFieldPolicySchema(nil), definition.SensitiveFieldPolicies...),
			ExportControls:         append([]reportmodel.ReportExportControlSchema(nil), definition.ExportControls...),
		})
	}
	version := strings.TrimSpace(revision)
	if version == "" {
		version = "1"
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		sourceID = "project"
	}
	return repository.SyncDefinitions(ctx, reportpersistence.DefinitionSnapshot{SchemaVersion: version, SourceKind: "project_registry", SourceID: sourceID, Definitions: definitions})
}
