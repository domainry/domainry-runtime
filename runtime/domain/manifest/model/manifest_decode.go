package manifestmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const CurrentManifestSchemaVersion = "2"

// DecodeManifest accepts only the current source-owned contract. The product
// has no released legacy manifest format, so compatibility rewriting would
// incorrectly make retired frontend fields part of the backend protocol.
func DecodeManifest(raw []byte) (ManifestSchema, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest ManifestSchema
	if err := decoder.Decode(&manifest); err != nil {
		return ManifestSchema{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return ManifestSchema{}, fmt.Errorf("manifest contains trailing JSON values")
		}
		return ManifestSchema{}, err
	}
	if version := strings.TrimSpace(manifest.SchemaVersion); version != CurrentManifestSchemaVersion {
		return ManifestSchema{}, fmt.Errorf("unsupported manifest schema_version %q", version)
	}
	hash, err := ManifestContentHash(manifest)
	if err != nil {
		return ManifestSchema{}, fmt.Errorf("hash manifest: %w", err)
	}
	if declared := strings.TrimSpace(manifest.ManifestHash); declared != "" && declared != hash {
		return ManifestSchema{}, fmt.Errorf("manifest_hash %q does not match canonical content hash %q", declared, hash)
	}
	manifest.ManifestHash = hash
	if err := VerifyManifestDefinitionVersion(manifest); err != nil {
		return ManifestSchema{}, fmt.Errorf("verify manifest definition version: %w", err)
	}
	return manifest, nil
}
