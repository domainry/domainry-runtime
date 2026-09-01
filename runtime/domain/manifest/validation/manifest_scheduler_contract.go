package validation

import (
	"context"
	"fmt"
	"strings"

	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/schedule"
)

func (state *validationState) validateSchedulerOwnershipAndDefinitions() {
	for index, object := range state.manifest.Objects {
		key := strings.TrimSpace(object.Key)
		if schedulersdk.OwnsManifestObjectKey(key) {
			state.add(fmt.Sprintf("objects[%d].key", index), "owner-managed Scheduler object %q must not be declared in a Runtime manifest", key)
		}
	}
	for index, seed := range state.manifest.SeedRecords {
		key := strings.TrimSpace(seed.ObjectKey)
		if schedulersdk.OwnsManifestObjectKey(key) {
			state.add(fmt.Sprintf("seed_records[%d].object_key", index), "Scheduler-owned operational state %q cannot be seeded by Runtime", key)
		}
	}

	seen := map[string]int{}
	for index, definition := range state.manifest.SchedulerDefinitions {
		path := fmt.Sprintf("scheduler_definitions[%d]", index)
		key := manifestSchedulerString(definition, "key")
		if key == "" {
			state.add(path+".key", "is required")
		} else if first, duplicate := seen[key]; duplicate {
			state.add(path+".key", "duplicate Scheduler definition key %q; first declared at scheduler_definitions[%d]", key, first)
		} else {
			seen[key] = index
		}
		if err := schedule.ValidateDefinitionData(context.Background(), definition); err != nil {
			code := schedule.ValidationCode(err)
			if code == "" {
				code = err.Error()
			}
			state.add(path, "violates Scheduler authoring contract: %s", code)
			continue
		}
		state.validateSchedulerTargetReference(path, definition)
	}
}

func (state *validationState) validateSchedulerTargetReference(path string, definition map[string]any) {
	targetType := strings.ToLower(manifestSchedulerString(definition, "target_type"))
	targetKey := manifestSchedulerString(definition, "target_key")
	switch targetType {
	case "workflow":
		workflowKey := strings.TrimPrefix(targetKey, "scheduled:")
		if workflowKey == "*" {
			return
		}
		for _, workflow := range state.manifest.Workflows {
			if strings.TrimSpace(workflow.Key) != workflowKey {
				continue
			}
			if !workflow.Enabled || workflow.TriggerContract == nil || strings.TrimSpace(workflow.TriggerContract.Type) != "scheduled" {
				state.add(path+".target_key", "workflow %q must be enabled with trigger_contract.type scheduled", workflowKey)
			}
			return
		}
		state.add(path+".target_key", "references unknown workflow %q", workflowKey)
	case "report_snapshot_refresh":
		for _, report := range state.manifest.Reports {
			if strings.TrimSpace(report.Key) == targetKey {
				return
			}
		}
		state.add(path+".target_key", "references unknown report %q", targetKey)
	case "http":
		connectionKey := manifestSchedulerString(definition, "connection_key")
		for _, connection := range state.manifest.Integrations.Connections {
			if strings.TrimSpace(connection.Key) == connectionKey {
				return
			}
		}
		state.add(path+".connection_key", "references unknown Integration connection %q", connectionKey)
	}
}

func manifestSchedulerString(data map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}
