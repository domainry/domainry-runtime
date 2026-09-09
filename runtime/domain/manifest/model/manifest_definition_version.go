package manifestmodel

import (
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
)

// ContentDerivedVersionPrefix marks a manifest version that Plane derived from
// the canonical manifest content. Metadata rejects a definition whose content
// changed under an unchanged schema_version, so a content-derived version must
// remain a pure function of the manifest body.
const ContentDerivedVersionPrefix = "0.0.0+manifest."

// DefinitionVersionNotContentDerivedCode is raised when a manifest carries the
// content-derived prefix but its version no longer matches the manifest body.
const DefinitionVersionNotContentDerivedCode = "backend.metadata.definition_version_not_content_derived"

// ManifestDefinitionVersion derives the content version of a manifest: the
// prefix followed by ManifestContentHash of the manifest with an empty Version
// and ManifestHash.
func ManifestDefinitionVersion(manifest ManifestSchema) (string, error) {
	manifest.Version = ""
	manifest.ManifestHash = ""
	hash, err := ManifestContentHash(manifest)
	if err != nil {
		return "", err
	}
	return ContentDerivedVersionPrefix + hash, nil
}

// VerifyManifestDefinitionVersion accepts any version that does not carry the
// content-derived prefix; a prefixed version must equal the derived value.
func VerifyManifestDefinitionVersion(manifest ManifestSchema) error {
	declared := strings.TrimSpace(manifest.Version)
	if !strings.HasPrefix(declared, ContentDerivedVersionPrefix) {
		return nil
	}
	expected, err := ManifestDefinitionVersion(manifest)
	if err != nil {
		return err
	}
	if declared != expected {
		return &apperror.CodedError{Code: DefinitionVersionNotContentDerivedCode, Params: map[string]string{"declared_version": declared, "expected_version": expected}}
	}
	return nil
}
