package manifestmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const CurrentManifestSchemaVersion = "2"

type ManifestMigrationWarning struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type LegacyFrontendReference struct {
	Kind       string   `json:"kind"`
	Key        string   `json:"key"`
	Roles      []string `json:"roles,omitempty"`
	Objects    []string `json:"objects,omitempty"`
	Views      []string `json:"views,omitempty"`
	Actions    []string `json:"actions,omitempty"`
	Reports    []string `json:"reports,omitempty"`
	Fields     []string `json:"fields,omitempty"`
	Permission string   `json:"permission,omitempty"`
	Route      string   `json:"route,omitempty"`
}

type ManifestMigrationReport struct {
	Migrated                 bool                       `json:"migrated"`
	FromVersion              string                     `json:"from_version"`
	ToVersion                string                     `json:"to_version"`
	Warnings                 []ManifestMigrationWarning `json:"warnings"`
	LegacyFrontendReferences []LegacyFrontendReference  `json:"legacy_frontend_references,omitempty"`
}

// DecodeManifest decodes the canonical v2 contract strictly. A v1 or
// versionless manifest is migrated once: retired frontend payload is stripped,
// domain references are returned as auditable migration evidence, and the
// caller receives a v2 manifest. Deprecated keys in an authored v2 manifest
// are rejected as unknown fields.
func DecodeManifest(raw []byte) (ManifestSchema, ManifestMigrationReport, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ManifestSchema{}, ManifestMigrationReport{}, err
	}
	version := rawJSONString(envelope["schema_version"])
	report := ManifestMigrationReport{FromVersion: version, ToVersion: CurrentManifestSchemaVersion, Warnings: []ManifestMigrationWarning{}, LegacyFrontendReferences: []LegacyFrontendReference{}}
	if version == "" || version == "1" {
		report.Migrated = true
		if version == "" {
			report.FromVersion = "1"
		}
		migrateLegacyFrontendEnvelope(envelope, &report)
		envelope["schema_version"] = json.RawMessage(`"` + CurrentManifestSchemaVersion + `"`)
		raw, _ = json.Marshal(envelope)
	} else if version != CurrentManifestSchemaVersion {
		return ManifestSchema{}, report, fmt.Errorf("unsupported manifest schema_version %q", version)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest ManifestSchema
	if err := decoder.Decode(&manifest); err != nil {
		return ManifestSchema{}, report, err
	}
	if report.Migrated {
		manifest.ManifestHash = ""
		hash, _ := ManifestContentHash(manifest)
		manifest.ManifestHash = hash
	}
	sort.Slice(report.LegacyFrontendReferences, func(i, j int) bool {
		return report.LegacyFrontendReferences[i].Kind+report.LegacyFrontendReferences[i].Key < report.LegacyFrontendReferences[j].Kind+report.LegacyFrontendReferences[j].Key
	})
	return manifest, report, nil
}

func migrateLegacyFrontendEnvelope(envelope map[string]json.RawMessage, report *ManifestMigrationReport) {
	if _, exists := envelope["roles"]; exists {
		delete(envelope, "roles")
		report.Warnings = append(report.Warnings, ManifestMigrationWarning{
			Code:    "manifest.v1.identity_payload_removed",
			Path:    "/roles",
			Message: "legacy role policy was removed; publish roles and authorization policy through Identity",
		})
	}
	if _, exists := envelope["identity_bootstrap"]; exists {
		delete(envelope, "identity_bootstrap")
		report.Warnings = append(report.Warnings, ManifestMigrationWarning{
			Code:    "manifest.v1.identity_payload_removed",
			Path:    "/identity_bootstrap",
			Message: "legacy Identity bootstrap data was removed; provision users, organization units, roles, and policy through Identity",
		})
	}
	if _, exists := envelope["users"]; exists {
		delete(envelope, "users")
		report.Warnings = append(report.Warnings, ManifestMigrationWarning{
			Code:    "manifest.v1.identity_payload_removed",
			Path:    "/users",
			Message: "legacy user bootstrap data was removed; provision users and assignments through Identity",
		})
	}
	for _, key := range []string{"frontend", "surfaces", "components", "views"} {
		raw, exists := envelope[key]
		if !exists {
			continue
		}
		if key == "surfaces" || key == "components" {
			extractLegacyFrontendReferences(key, raw, report)
		}
		delete(envelope, key)
		report.Warnings = append(report.Warnings, ManifestMigrationWarning{Code: "manifest.v1.frontend_payload_removed", Path: "/" + key, Message: "legacy frontend payload was removed; use Builder design, route and deployment evidence"})
	}
	if raw, exists := envelope["menus"]; exists {
		extractLegacyMenuReferences(raw, report)
		delete(envelope, "menus")
		report.Warnings = append(report.Warnings, ManifestMigrationWarning{Code: "manifest.v1.menu_payload_removed", Path: "/menus", Message: "domain navigation belongs to the source-owned frontend Route Registry"})
	}
	if _, exists := envelope["entrypoints"]; exists {
		delete(envelope, "entrypoints")
		report.Warnings = append(report.Warnings, ManifestMigrationWarning{Code: "manifest.v1.entrypoints_removed", Path: "/entrypoints", Message: "frontend routing belongs to the source-owned Route Registry"})
	}
	var actions []map[string]json.RawMessage
	if raw := envelope["actions"]; len(raw) > 0 && json.Unmarshal(raw, &actions) == nil {
		changed := false
		for index := range actions {
			if _, exists := actions[index]["idempotency_keys"]; exists {
				delete(actions[index], "idempotency_keys")
				changed = true
				report.Warnings = append(report.Warnings, ManifestMigrationWarning{Code: "manifest.v1.action_idempotency_field_removed", Path: fmt.Sprintf("/actions/%d/idempotency_keys", index), Message: "Action request idempotency is enforced by the invocation protocol"})
			}
			for _, key := range []string{"ui_placement", "confirmation"} {
				if _, exists := actions[index][key]; !exists {
					continue
				}
				delete(actions[index], key)
				changed = true
				report.Warnings = append(report.Warnings, ManifestMigrationWarning{Code: "manifest.v1.action_frontend_field_removed", Path: fmt.Sprintf("/actions/%d/%s", index, key), Message: "Action placement and confirmation copy belong to the source-owned frontend"})
			}
			var config map[string]json.RawMessage
			if rawConfig := actions[index]["config"]; len(rawConfig) > 0 && json.Unmarshal(rawConfig, &config) == nil {
				if _, exists := config["confirmation"]; exists {
					delete(config, "confirmation")
					actions[index]["config"], _ = json.Marshal(config)
					changed = true
					report.Warnings = append(report.Warnings, ManifestMigrationWarning{Code: "manifest.v1.action_frontend_field_removed", Path: fmt.Sprintf("/actions/%d/config/confirmation", index), Message: "Action confirmation copy belongs to the source-owned frontend"})
				}
			}
		}
		if changed {
			envelope["actions"], _ = json.Marshal(actions)
		}
	}
}

func extractLegacyMenuReferences(raw json.RawMessage, report *ManifestMigrationReport) {
	var values []map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return
	}
	for _, value := range values {
		ref := LegacyFrontendReference{Kind: "menu", Key: mapText(value, "key"), Roles: mapStrings(value, "roles"), Permission: mapText(value, "required_permission"), Route: mapText(value, "route")}
		ref.Objects = migrationCompactStrings(append(mapStrings(value, "object_keys"), nestedMapText(value, "target", "object_key")))
		if pageKey := nestedMapText(value, "target", "page_key"); pageKey != "" {
			ref.Views = []string{pageKey}
		}
		if ref.Key != "" {
			report.LegacyFrontendReferences = append(report.LegacyFrontendReferences, ref)
		}
	}
}

func nestedMapText(value map[string]any, key, nestedKey string) string {
	nested, _ := value[key].(map[string]any)
	return mapText(nested, nestedKey)
}

func extractLegacyFrontendReferences(kind string, raw json.RawMessage, report *ManifestMigrationReport) {
	var values []map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return
	}
	for _, value := range values {
		ref := LegacyFrontendReference{Kind: strings.TrimSuffix(kind, "s"), Key: mapText(value, "key")}
		ref.Roles = mapStrings(value, "roles")
		ref.Objects = mapStrings(value, "object_keys")
		ref.Actions = append(mapStrings(value, "action_keys"), mapText(value, "primary_action"))
		ref.Reports = mapStrings(value, "report_keys")
		ref.Views = migrationCompactStrings([]string{mapText(value, "view_key")})
		ref.Fields = migrationCompactStrings([]string{mapText(value, "status_field"), mapText(value, "timeline_field")})
		ref.Permission = mapText(value, "permission")
		ref.Actions = migrationCompactStrings(ref.Actions)
		if ref.Key != "" {
			report.LegacyFrontendReferences = append(report.LegacyFrontendReferences, ref)
		}
	}
}

func rawJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return strings.TrimSpace(value)
}

func mapText(value map[string]any, key string) string {
	raw, exists := value[key]
	if !exists || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func mapStrings(value map[string]any, key string) []string {
	items, _ := value[key].([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, strings.TrimSpace(fmt.Sprint(item)))
	}
	return migrationCompactStrings(out)
}

func migrationCompactStrings(values []string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "<nil>" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
