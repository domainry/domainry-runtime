package service

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	preferencemodel "github.com/domainry/domainry-runtime/runtime/domain/preference/model"
)

func ResolveWorkspacePreference(versions []preferencemodel.WorkspacePreferenceVersion, workspaceID, preferenceKey string, effectiveAt time.Time) (preferencemodel.WorkspacePreferenceResolution, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return preferencemodel.WorkspacePreferenceResolution{}, preferenceResolutionError("backend.preference.workspace_required", preferenceKey)
	}
	preferenceKey = strings.TrimSpace(preferenceKey)
	if preferenceKey == "" {
		return preferencemodel.WorkspacePreferenceResolution{}, preferenceResolutionError("backend.preference.key_required", preferenceKey)
	}
	effectiveAt = effectiveAt.UTC()
	eligible := make([]preferencemodel.WorkspacePreferenceVersion, 0, len(versions))
	for _, version := range versions {
		if strings.TrimSpace(version.WorkspaceID) != workspaceID || strings.TrimSpace(version.Definition.Key) != preferenceKey {
			continue
		}
		from, err := preferencemodel.ParsePreferenceEffectiveTime(version.Definition.EffectiveFrom)
		if err != nil {
			return preferencemodel.WorkspacePreferenceResolution{}, preferenceResolutionError("backend.preference.effective_from_invalid", preferenceKey)
		}
		if effectiveAt.Before(from) {
			continue
		}
		if strings.TrimSpace(version.Definition.EffectiveTo) != "" {
			to, parseErr := preferencemodel.ParsePreferenceEffectiveTime(version.Definition.EffectiveTo)
			if parseErr != nil {
				return preferencemodel.WorkspacePreferenceResolution{}, preferenceResolutionError("backend.preference.effective_to_invalid", preferenceKey)
			}
			if !effectiveAt.Before(to) {
				continue
			}
		}
		if parsedVersion, parseErr := strconv.Atoi(strings.TrimSpace(version.Version)); parseErr != nil || parsedVersion < 1 {
			return preferencemodel.WorkspacePreferenceResolution{}, preferenceResolutionError("backend.preference.version_invalid", preferenceKey)
		}
		eligible = append(eligible, version)
	}
	if len(eligible) == 0 {
		return preferencemodel.WorkspacePreferenceResolution{}, preferenceResolutionError("backend.preference.effective_version_not_found", preferenceKey)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left, _ := preferencemodel.ParsePreferenceEffectiveTime(eligible[i].Definition.EffectiveFrom)
		right, _ := preferencemodel.ParsePreferenceEffectiveTime(eligible[j].Definition.EffectiveFrom)
		if !left.Equal(right) {
			return left.After(right)
		}
		leftVersion, _ := strconv.Atoi(strings.TrimSpace(eligible[i].Version))
		rightVersion, _ := strconv.Atoi(strings.TrimSpace(eligible[j].Version))
		if leftVersion != rightVersion {
			return leftVersion > rightVersion
		}
		return eligible[i].ResourceHash > eligible[j].ResourceHash
	})
	selected := eligible[0]
	decoder := json.NewDecoder(bytes.NewReader(selected.Definition.Value))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return preferencemodel.WorkspacePreferenceResolution{}, preferenceResolutionError("backend.preference.value_invalid", preferenceKey)
	}
	return preferencemodel.WorkspacePreferenceResolution{
		WorkspaceID: selected.WorkspaceID, PreferenceKey: preferenceKey, ValueType: selected.Definition.ValueType, Value: value,
		Version: selected.Version, ResourceHash: selected.ResourceHash, EffectiveAt: effectiveAt.Format(time.RFC3339),
		EffectiveFrom: selected.Definition.EffectiveFrom, EffectiveTo: selected.Definition.EffectiveTo,
	}, nil
}

func preferenceResolutionError(code, key string) error {
	return &preferencemodel.WorkspacePreferenceError{Code: code, PreferenceKey: key}
}
