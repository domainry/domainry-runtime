package metadata

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (h *MetadataHandler) getMetadataDefinition(w http.ResponseWriter, r *http.Request) {
	resourceType, resourceKey := strings.TrimSpace(r.PathValue("resourceType")), strings.TrimSpace(r.PathValue("resourceKey"))
	definition, ok, err := h.definitions.GetMetadataDefinition(r.Context(), resourceType, resourceKey, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !ok {
		h.writeError(w, r, http.StatusNotFound, "metadata.definition.not_found")
		return
	}
	h.writeMetadataCachedJSON(w, r, map[string]any{"definition": definition})
}
func (h *MetadataHandler) listMetadataDefinitionVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.definitions.ListMetadataDefinitionVersions(r.Context(), strings.TrimSpace(r.PathValue("resourceType")), strings.TrimSpace(r.PathValue("resourceKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeMetadataCachedJSON(w, r, map[string]any{"versions": versions})
}
func (h *MetadataHandler) diffMetadataDefinitionVersions(w http.ResponseWriter, r *http.Request) {
	resourceType, resourceKey := strings.TrimSpace(r.PathValue("resourceType")), strings.TrimSpace(r.PathValue("resourceKey"))
	fromVersion, toVersion := strings.TrimSpace(r.URL.Query().Get("from")), strings.TrimSpace(r.URL.Query().Get("to"))
	versions, err := h.definitions.ListMetadataDefinitionVersions(r.Context(), resourceType, resourceKey, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	var fromPayload, toPayload json.RawMessage
	for _, v := range versions {
		if v.SchemaVersion == fromVersion {
			fromPayload = v.Payload
		}
		if v.SchemaVersion == toVersion || toVersion == "" {
			toPayload = v.Payload
			if toVersion == "" {
				toVersion = v.SchemaVersion
			}
		}
	}
	h.writeMetadataCachedJSON(w, r, map[string]any{"resource_type": resourceType, "resource_key": resourceKey, "from_version": fromVersion, "to_version": toVersion, "from_payload": fromPayload, "to_payload": toPayload})
}
func metadataAuditPayload(payload json.RawMessage) map[string]any {
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return map[string]any{}
	}
	metadata := map[string]any{}
	for _, key := range []string{"key", "object_key", "type"} {
		if value, ok := decoded[key]; ok {
			metadata[key] = value
		}
	}
	if config, ok := decoded["config"].(map[string]any); ok {
		if registry, ok := config["view_registry"].(map[string]any); ok {
			for _, key := range []string{"scope", "locked", "default_for_object", "team_key", "migrated_from_local_storage"} {
				metadata[key] = registry[key]
			}
		}
	}
	return metadata
}
