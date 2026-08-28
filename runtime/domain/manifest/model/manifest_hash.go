package manifestmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ManifestContentHash is stable across serialization formatting and excludes
// the manifest_hash field itself.
func ManifestContentHash(manifest ManifestSchema) (string, error) {
	manifest.ManifestHash = ""
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
