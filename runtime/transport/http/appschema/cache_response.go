package appschema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

func (h *ApplicationSchemaHandler) writeMetadataCachedJSON(w http.ResponseWriter, r *http.Request, payload any) {
	etag := metadataPayloadETag(payload)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=5, must-revalidate")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.writeJSON(w, http.StatusOK, payload)
}
func metadataPayloadETag(payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte("metadata")
	}
	sum := sha256.Sum256(raw)
	return `"` + hex.EncodeToString(sum[:])[:24] + `"`
}
