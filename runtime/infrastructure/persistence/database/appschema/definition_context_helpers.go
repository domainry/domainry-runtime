package appschema

import (
	"crypto/sha256"
	"encoding/hex"
)

func metadataDefinitionRefreshIntentID(resourceType, resourceKey, schemaHash string) string {
	sum := sha256.Sum256([]byte(resourceType + "\x00" + resourceKey + "\x00" + schemaHash))
	return "boundary_intent:" + hex.EncodeToString(sum[:])[:24]
}
