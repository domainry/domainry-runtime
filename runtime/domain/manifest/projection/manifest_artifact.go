package projection

import (
	"fmt"
	"sort"
	"strings"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type ReviewArtifactOptions struct {
	Title string
}

func RenderManifestReviewMarkdown(manifest manifestmodel.ManifestSchema, opts ReviewArtifactOptions) string {
	var out strings.Builder
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = strings.TrimSpace(manifest.Name)
	}
	if title == "" {
		title = strings.TrimSpace(manifest.TemplateID)
	}
	writeLine(&out, "# "+valueOrFallback(title, "Business System Review"))
	writeLine(&out, "")
	writeLine(&out, "## Scope")
	writeLine(&out, "")
	writeKV(&out, "Template", manifest.TemplateID)
	writeKV(&out, "Version", manifest.Version)
	writeKV(&out, "Name", manifest.Name)
	writeLine(&out, "")
	writeObjectModel(&out, manifest)
	writeWorkflowsActionsReports(&out, manifest)
	writeSeedScenarios(&out, manifest)
	return strings.TrimRight(out.String(), "\n") + "\n"
}

func writeObjectModel(out *strings.Builder, manifest manifestmodel.ManifestSchema) {
	writeLine(out, "## Object Model")
	writeLine(out, "")
	for _, object := range manifest.Objects {
		writeLine(out, "### "+valueOrFallback(object.Name, object.Key))
		writeLine(out, "")
		writeKV(out, "Key", object.Key)
		if object.Description != "" {
			writeKV(out, "Description", object.Description)
		}
		writeLine(out, "")
		writeLine(out, "| Field | Type | Required | Options/Target |")
		writeLine(out, "| --- | --- | --- | --- |")
		for _, field := range object.Fields {
			writeLine(out, fmt.Sprintf("| `%s` %s | `%s` | %t | %s |", field.Key, field.Name, field.Type, field.Required, fieldEvidence(field)))
		}
		writeLine(out, "")
	}
}

func writeWorkflowsActionsReports(out *strings.Builder, manifest manifestmodel.ManifestSchema) {
	writeLine(out, "## Actions Workflows And Reports")
	writeLine(out, "")
	writeLine(out, "| Type | Key | Object/Source | Permission/Run As |")
	writeLine(out, "| --- | --- | --- | --- |")
	for _, action := range manifest.Actions {
		writeLine(out, fmt.Sprintf("| Action | `%s` %s | `%s` | `%s` |", action.Key, action.Label, action.ObjectKey, action.RequiresPermission))
	}
	for _, workflow := range manifest.Workflows {
		writeLine(out, fmt.Sprintf("| Workflow | `%s` %s | %s | `%s` |", workflow.Key, workflow.Name, codeList(manifestWorkflowObjectKeys(workflow)), workflow.RunAs))
	}
	for _, report := range manifest.Reports {
		writeLine(out, fmt.Sprintf("| Report | `%s` %s | %s | %s |", report.Key, report.Name, codeList(reportmodel.ReportDatasetObjectKeys(report.Dataset)), codeList(report.RequiredPermissions)))
	}
	writeLine(out, "")
}

func writeSeedScenarios(out *strings.Builder, manifest manifestmodel.ManifestSchema) {
	writeLine(out, "## Seed Scenarios")
	writeLine(out, "")
	writeLine(out, "| Object | Seed Key | Covered Fields | Relation References |")
	writeLine(out, "| --- | --- | --- | --- |")
	for index, seed := range manifest.SeedRecords {
		writeLine(out, fmt.Sprintf("| `%s` | `%s` | %s | %s |", seed.ObjectKey, manifestSeedKey(seed, index), codeList(sortedMapKeys(seed.Data, "__seed_key")), codeList(seedRecordRefs(seed.Data))))
	}
	writeLine(out, "")
}

func fieldEvidence(field definitionmodel.FieldSchema) string {
	parts := []string{}
	if target := strings.TrimSpace(field.Validation.Target); target != "" {
		parts = append(parts, "target `"+target+"`")
	}
	if dictionary := manifestMapString(field.Config, "dictionary_key"); dictionary != "" {
		parts = append(parts, "dictionary `"+dictionary+"`")
	}
	for _, option := range manifestFieldAllowedValues(field) {
		parts = append(parts, "`"+option+"`")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func sortedMapKeys(data map[string]any, omit ...string) []string {
	omitSet := map[string]bool{}
	for _, key := range omit {
		omitSet[key] = true
	}
	keys := make([]string, 0, len(data))
	for key := range data {
		if !omitSet[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func seedRecordRefs(data map[string]any) []string {
	refs := map[string]bool{}
	collectSeedRefsFromValue(data, refs)
	out := make([]string, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func collectSeedRefsFromValue(value any, refs map[string]bool) {
	switch typed := value.(type) {
	case string:
		if ref := strings.TrimPrefix(typed, "$record:"); ref != typed && strings.TrimSpace(ref) != "" {
			refs[strings.TrimSpace(ref)] = true
		}
	case []any:
		for _, item := range typed {
			collectSeedRefsFromValue(item, refs)
		}
	case map[string]any:
		for _, item := range typed {
			collectSeedRefsFromValue(item, refs)
		}
	}
}

func writeKV(out *strings.Builder, key string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	writeLine(out, "- "+key+": `"+strings.TrimSpace(value)+"`")
}

func writeLine(out *strings.Builder, line string) {
	out.WriteString(line)
	out.WriteByte('\n')
}

func codeList(values []string) string {
	compacted := manifestCompactStrings(values)
	if len(compacted) == 0 {
		return ""
	}
	for index, value := range compacted {
		compacted[index] = "`" + value + "`"
	}
	return strings.Join(compacted, ", ")
}

func valueOrFallback(value string, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func manifestSeedKey(seed businessseedmodel.SeedRecordSchema, index int) string {
	if seed.Data != nil {
		key := strings.TrimSpace(fmt.Sprint(seed.Data["__seed_key"]))
		if key != "" && key != "<nil>" {
			return key
		}
	}
	if objectKey := strings.TrimSpace(seed.ObjectKey); objectKey != "" {
		return fmt.Sprintf("%s_seed_%03d", objectKey, index+1)
	}
	return ""
}

func manifestMapString(value map[string]any, key string) string {
	if value == nil || value[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value[key]))
}

func manifestCompactStrings(values []string) []string {
	out := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func manifestFieldAllowedValues(field definitionmodel.FieldSchema) []string {
	values := append([]string{}, manifestCompactStrings(field.Validation.Options)...)
	switch typed := field.Options.(type) {
	case []any:
		for _, item := range typed {
			if option, ok := item.(map[string]any); ok {
				values = append(values, manifestMapString(option, "value"))
			}
		}
	case []map[string]any:
		for _, item := range typed {
			values = append(values, manifestMapString(item, "value"))
		}
	}
	return manifestCompactStrings(values)
}

func manifestWorkflowObjectKeys(workflow definitionmodel.WorkflowSchema) []string {
	keys := []string{}
	if workflow.TriggerContract != nil {
		if key := strings.TrimSpace(workflow.TriggerContract.ObjectKey); key != "" {
			keys = append(keys, key)
		}
		keys = append(keys, workflow.TriggerContract.ObjectKeys...)
	}
	if key := manifestMapString(workflow.Trigger, "object_key"); key != "" {
		keys = append(keys, key)
	}
	if raw, ok := workflow.Trigger["object_keys"].([]any); ok {
		for _, item := range raw {
			if key := strings.TrimSpace(fmt.Sprint(item)); key != "" {
				keys = append(keys, key)
			}
		}
	}
	return keys
}
