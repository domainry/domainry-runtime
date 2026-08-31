package validation

import (
	"fmt"
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
)

func (state *validationState) validateSeedRecords() {
	if len(state.manifest.SeedRecords) == 0 {
		state.add("seed_records", "domain schema must declare seed data")
		return
	}
	optionalProfileObjects := map[string]bool{}
	for _, extension := range state.manifest.IdentityProfileExtensions {
		optionalProfileObjects[extension.ObjectKey] = true
	}
	for objectKey := range state.objects {
		if optionalProfileObjects[objectKey] {
			continue
		}
		if runtimeOwned, _ := state.objects[objectKey].Config["runtime_owned"].(bool); runtimeOwned {
			continue
		}
		if !state.seeded[objectKey] {
			state.add("seed_records", "object %q has required fields but no seed record", objectKey)
		}
	}
	for index, seed := range state.manifest.SeedRecords {
		path := fmt.Sprintf("seed_records[%d]", index)
		objectKey := strings.TrimSpace(seed.ObjectKey)
		if strings.HasPrefix(objectKey, "identity_") {
			state.add(path+".object_key", "platform Identity resources cannot be created through domain seed_records; provision them through Identity")
			continue
		}
		object := state.objects[objectKey]
		if object.Key == "" {
			state.add(path+".object_key", "unknown object %q", seed.ObjectKey)
			continue
		}
		for _, field := range object.Fields {
			if field.Required && seed.Data[field.Key] == nil && field.Default == nil && field.DefaultValue == nil {
				state.add(path+".data."+field.Key, "required seed field is missing")
			}
			state.validateSeedFieldValue(path+".data."+field.Key, objectKey, field, seed.Data[field.Key])
		}
		for dataKey := range seed.Data {
			if dataKey != "__seed_key" && state.fields[objectKey][dataKey].Key == "" {
				state.add(path+".data."+dataKey, "unknown seed field")
			}
		}
	}
	for _, report := range state.manifest.Reports {
		for _, objectKey := range reportmodel.ReportDatasetObjectKeys(report.Dataset) {
			if !state.seeded[objectKey] {
				state.add("seed_records", "report %q source object %q has no seed evidence", report.Key, objectKey)
			}
		}
		for evidenceIndex, requirement := range report.EvidenceRequirements {
			qualified := 0
			for _, seed := range state.manifest.SeedRecords {
				if strings.TrimSpace(seed.ObjectKey) != strings.TrimSpace(requirement.ObjectKey) {
					continue
				}
				matches := true
				for _, fieldKey := range requirement.RequiredNonEmptyFields {
					if recordcontract.RecordIsEmptyValue(seed.Data[strings.TrimSpace(fieldKey)]) {
						matches = false
						break
					}
				}
				if matches {
					qualified++
				}
			}
			if qualified < requirement.MinimumRecords {
				state.add(fmt.Sprintf("reports[%s].evidence_requirements[%d]", report.Key, evidenceIndex), "requires at least %d qualifying seed records, found %d", requirement.MinimumRecords, qualified)
			}
		}
	}
	for _, action := range state.manifest.Actions {
		if !state.seeded[action.ObjectKey] {
			state.add("seed_records", "action %q object %q has no seed evidence", action.Key, action.ObjectKey)
		}
	}
}

func hasNonEmptyString(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func (state *validationState) validateSeedFieldValue(path, objectKey string, field definitionmodel.FieldSchema, value any) {
	if value == nil {
		return
	}
	if field.Type == "relation" {
		ref := strings.TrimPrefix(strings.TrimSpace(fmt.Sprint(value)), "$record:")
		if ref != fmt.Sprint(value) && strings.TrimSpace(ref) != "" {
			targetObject := strings.TrimSpace(field.Validation.Target)
			if targetObject != "" && state.seeds[ref] != "" && state.seeds[ref] != targetObject {
				state.add(path, "references seed %q from object %q, expected %q", ref, state.seeds[ref], targetObject)
			}
			if state.seeds[ref] == "" {
				state.add(path, "references unknown seed key %q", ref)
			}
		}
	}
	if allowed := fieldAllowedValues(field); len(allowed) > 0 {
		stored := strings.TrimSpace(fmt.Sprint(value))
		if stored != "" && !containsString(allowed, stored) {
			state.add(path, "seed option value %q is not in allowed values", stored)
		}
	}
}
