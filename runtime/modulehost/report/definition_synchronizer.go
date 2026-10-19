package reportmodulehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// Reports are code-owned, so their payload can change without model.json
	// changing. Include all report content in the immutable definition version.
	raw, err := json.Marshal(definitions)
	if err != nil {
		return fmt.Errorf("encode Report definition snapshot: %w", err)
	}
	hash := sha256.Sum256(raw)
	version += ":" + hex.EncodeToString(hash[:])
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		sourceID = "project"
	}
	return repository.SyncDefinitions(ctx, reportpersistence.DefinitionSnapshot{SchemaVersion: version, SourceKind: "project_registry", SourceID: sourceID, Definitions: definitions})
}
