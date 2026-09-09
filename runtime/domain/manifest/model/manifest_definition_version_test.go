package manifestmodel

import (
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestManifestDefinitionVersionIsContentDerivedAndIgnoresVersionAndHash(t *testing.T) {
	manifest := ManifestSchema{SchemaVersion: CurrentManifestSchemaVersion, TemplateID: "crm", Version: "anything", ManifestHash: "hash"}
	version, err := ManifestDefinitionVersion(manifest)
	if err != nil || !strings.HasPrefix(version, ContentDerivedVersionPrefix) || len(version) != len(ContentDerivedVersionPrefix)+64 {
		t.Fatalf("version=%q err=%v", version, err)
	}
	manifest.Version, manifest.ManifestHash = "other", "other-hash"
	again, err := ManifestDefinitionVersion(manifest)
	if err != nil || again != version {
		t.Fatalf("version changed with version/hash fields: %q vs %q err=%v", again, version, err)
	}
	manifest.TemplateID = "erp"
	changed, err := ManifestDefinitionVersion(manifest)
	if err != nil || changed == version {
		t.Fatalf("content change did not change the derived version: %q err=%v", changed, err)
	}
}

func TestVerifyManifestDefinitionVersion(t *testing.T) {
	manifest := ManifestSchema{SchemaVersion: CurrentManifestSchemaVersion, TemplateID: "crm", Version: "1"}
	if err := VerifyManifestDefinitionVersion(manifest); err != nil {
		t.Fatalf("prefix-less version rejected: %v", err)
	}
	derived, err := ManifestDefinitionVersion(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = derived
	manifest.ManifestHash = "declared"
	if err := VerifyManifestDefinitionVersion(manifest); err != nil {
		t.Fatalf("content-derived version rejected: %v", err)
	}
	manifest.Version = ContentDerivedVersionPrefix + strings.Repeat("0", 64)
	err = VerifyManifestDefinitionVersion(manifest)
	var coded *apperror.CodedError
	if !errors.As(err, &coded) || coded.Code != DefinitionVersionNotContentDerivedCode || coded.Params["expected_version"] != derived || coded.Params["declared_version"] != manifest.Version {
		t.Fatalf("tampered version error=%v", err)
	}
}

func TestDecodeManifestVerifiesContentDerivedVersion(t *testing.T) {
	base, err := DecodeManifest([]byte(`{"schema_version":"2","template_id":"crm","version":"1","objects":[],"roles":null}`))
	if err != nil {
		t.Fatal(err)
	}
	derived, err := ManifestDefinitionVersion(base)
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"schema_version":"2","template_id":"crm","version":"` + derived + `","objects":[],"roles":null}`
	decoded, err := DecodeManifest([]byte(valid))
	if err != nil || decoded.Version != derived {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	tampered := `{"schema_version":"2","template_id":"erp","version":"` + derived + `","objects":[],"roles":null}`
	if _, err := DecodeManifest([]byte(tampered)); err == nil || !strings.Contains(err.Error(), DefinitionVersionNotContentDerivedCode) {
		t.Fatalf("tampered manifest err=%v", err)
	}
	plain := `{"schema_version":"2","template_id":"erp","version":"1.2.3","objects":[],"roles":null}`
	if _, err := DecodeManifest([]byte(plain)); err != nil {
		t.Fatalf("plain version rejected: %v", err)
	}
}
