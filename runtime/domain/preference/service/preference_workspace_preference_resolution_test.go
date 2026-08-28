package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	preferencemodel "github.com/domainry/domainry-runtime/runtime/domain/preference/model"
)

func TestResolveWorkspacePreferenceSelectsEffectiveVersionAndPreservesEvidence(t *testing.T) {
	t.Parallel()

	versions := []preferencemodel.WorkspacePreferenceVersion{
		preferenceVersion("workspace-a", "policy.limit", "1", "hash-1", "2026-01-01", "2026-07-01", `10`),
		preferenceVersion("workspace-a", "policy.limit", "2", "hash-2", "2026-07-01", "", `20`),
		preferenceVersion("workspace-a", "policy.limit", "3", "hash-3", "2026-08-01", "", `30`),
		preferenceVersion("workspace-b", "policy.limit", "99", "hash-b", "2026-01-01", "", `999`),
	}

	resolved, err := ResolveWorkspacePreference(versions, "workspace-a", "policy.limit", mustPreferenceTime(t, "2026-07-21T02:03:04Z"))
	if err != nil {
		t.Fatalf("resolve preference: %v", err)
	}
	if resolved.WorkspaceID != "workspace-a" || resolved.Version != "2" || resolved.ResourceHash != "hash-2" || resolved.EffectiveFrom != "2026-07-01" || resolved.EffectiveAt != "2026-07-21T02:03:04Z" {
		t.Fatalf("unexpected resolution evidence: %+v", resolved)
	}
	if number, ok := resolved.Value.(json.Number); !ok || number.String() != "20" {
		t.Fatalf("expected lossless JSON number 20, got %#v", resolved.Value)
	}
}

func TestResolveWorkspacePreferenceUsesHighestVersionForSameEffectiveDate(t *testing.T) {
	t.Parallel()

	versions := []preferencemodel.WorkspacePreferenceVersion{
		preferenceVersion("workspace-a", "policy.mode", "9", "hash-9", "2026-01-01", "", `"old"`),
		preferenceVersion("workspace-a", "policy.mode", "10", "hash-10", "2026-01-01", "", `"new"`),
	}
	resolved, err := ResolveWorkspacePreference(versions, "workspace-a", "policy.mode", mustPreferenceTime(t, "2026-07-21T00:00:00Z"))
	if err != nil || resolved.Version != "10" || resolved.Value != "new" {
		t.Fatalf("expected numeric version 10, got result=%+v err=%v", resolved, err)
	}
}

func TestResolveWorkspacePreferenceFailsClosedOnScopeAndVersionEdges(t *testing.T) {
	t.Parallel()

	valid := preferenceVersion("workspace-a", "policy.limit", "1", "hash-1", "2026-01-01", "2026-07-01", `10`)
	tests := []struct {
		name      string
		versions  []preferencemodel.WorkspacePreferenceVersion
		workspace string
		key       string
		effective string
		code      string
	}{
		{name: "workspace required", versions: []preferencemodel.WorkspacePreferenceVersion{valid}, key: "policy.limit", effective: "2026-06-01T00:00:00Z", code: "backend.preference.workspace_required"},
		{name: "key required", versions: []preferencemodel.WorkspacePreferenceVersion{valid}, workspace: "workspace-a", effective: "2026-06-01T00:00:00Z", code: "backend.preference.key_required"},
		{name: "cross workspace is not visible", versions: []preferencemodel.WorkspacePreferenceVersion{valid}, workspace: "workspace-b", key: "policy.limit", effective: "2026-06-01T00:00:00Z", code: "backend.preference.effective_version_not_found"},
		{name: "before first version", versions: []preferencemodel.WorkspacePreferenceVersion{valid}, workspace: "workspace-a", key: "policy.limit", effective: "2025-12-31T23:59:59Z", code: "backend.preference.effective_version_not_found"},
		{name: "effective to is exclusive", versions: []preferencemodel.WorkspacePreferenceVersion{valid}, workspace: "workspace-a", key: "policy.limit", effective: "2026-07-01T00:00:00Z", code: "backend.preference.effective_version_not_found"},
		{name: "invalid stored version", versions: []preferencemodel.WorkspacePreferenceVersion{preferenceVersion("workspace-a", "policy.limit", "latest", "hash", "2026-01-01", "", `10`)}, workspace: "workspace-a", key: "policy.limit", effective: "2026-06-01T00:00:00Z", code: "backend.preference.version_invalid"},
		{name: "invalid stored effective date", versions: []preferencemodel.WorkspacePreferenceVersion{preferenceVersion("workspace-a", "policy.limit", "1", "hash", "invalid", "", `10`)}, workspace: "workspace-a", key: "policy.limit", effective: "2026-06-01T00:00:00Z", code: "backend.preference.effective_from_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveWorkspacePreference(test.versions, test.workspace, test.key, mustPreferenceTime(t, test.effective))
			var resolutionErr *preferencemodel.WorkspacePreferenceError
			if !errors.As(err, &resolutionErr) || resolutionErr.Code != test.code {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
		})
	}
}

func TestResolveWorkspacePreferenceCoversStoredBoundaryAndStableOrderingEdges(t *testing.T) {
	effective := mustPreferenceTime(t, "2026-06-01T00:00:00Z")

	resolved, err := ResolveWorkspacePreference([]preferencemodel.WorkspacePreferenceVersion{
		preferenceVersion("workspace-a", "other.key", "1", "other", "2026-01-01", "", `1`),
		preferenceVersion("workspace-a", "policy.limit", "1", "older", "2026-01-01", "2026-07-01", `10`),
		preferenceVersion("workspace-a", "policy.limit", "2", "newer", "2026-05-01", "", `20`),
	}, "workspace-a", "policy.limit", effective)
	if err != nil || resolved.ResourceHash != "newer" {
		t.Fatalf("different effective dates result=%#v err=%v", resolved, err)
	}

	resolved, err = ResolveWorkspacePreference([]preferencemodel.WorkspacePreferenceVersion{
		preferenceVersion("workspace-a", "policy.limit", "2", "hash-a", "2026-01-01", "", `20`),
		preferenceVersion("workspace-a", "policy.limit", "2", "hash-b", "2026-01-01", "", `21`),
	}, "workspace-a", "policy.limit", effective)
	if err != nil || resolved.ResourceHash != "hash-b" {
		t.Fatalf("stable hash ordering result=%#v err=%v", resolved, err)
	}

	for _, test := range []struct {
		name    string
		version preferencemodel.WorkspacePreferenceVersion
		code    string
	}{
		{name: "invalid effective to", version: preferenceVersion("workspace-a", "policy.limit", "1", "hash", "2026-01-01", "invalid", `10`), code: "backend.preference.effective_to_invalid"},
		{name: "zero version", version: preferenceVersion("workspace-a", "policy.limit", "0", "hash", "2026-01-01", "", `10`), code: "backend.preference.version_invalid"},
		{name: "invalid value", version: preferenceVersion("workspace-a", "policy.limit", "1", "hash", "2026-01-01", "", `{`), code: "backend.preference.value_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, resolveErr := ResolveWorkspacePreference([]preferencemodel.WorkspacePreferenceVersion{test.version}, "workspace-a", "policy.limit", effective)
			var resolutionErr *preferencemodel.WorkspacePreferenceError
			if !errors.As(resolveErr, &resolutionErr) || resolutionErr.Code != test.code {
				t.Fatalf("error=%v want=%s", resolveErr, test.code)
			}
		})
	}
}

func preferenceVersion(workspaceID, key, version, hash, from, to, value string) preferencemodel.WorkspacePreferenceVersion {
	return preferencemodel.WorkspacePreferenceVersion{
		WorkspaceID: workspaceID,
		Definition: preferencemodel.WorkspacePreferenceDefinition{
			Key: key, Name: key, ValueType: "integer", Value: json.RawMessage(value), EffectiveFrom: from, EffectiveTo: to,
		},
		Version: version, ResourceHash: hash,
	}
}

func mustPreferenceTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %s: %v", value, err)
	}
	return parsed
}
