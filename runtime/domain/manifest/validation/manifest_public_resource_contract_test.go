package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestPublicResourceContractClosesAnonymousProjectionAndVerifiedFiles(t *testing.T) {
	manifest := publicResourceContractManifest()
	state := newValidationState(manifest, nil)
	state.validateObjects()
	if len(state.errs) != 0 {
		t.Fatalf("valid public resource rejected: %v", state.errs)
	}

	invalid := publicResourceContractManifest()
	resource := &invalid.Objects[0].PublicResources[0]
	resource.Fields = append(resource.Fields, "share_key", "portrait_file", "secret_note")
	resource.Files = append(resource.Files, resource.Files[0])
	invalid.Objects[0].Fields = append(invalid.Objects[0].Fields, definitionmodel.FieldSchema{Key: "secret_note", Type: "text", Sensitive: true})
	state = newValidationState(invalid, nil)
	state.validateObjects()
	joined := state.errs.Error()
	for _, expected := range []string{"access and publication-state fields", "must use a files binding", "must be enabled and non-sensitive", "duplicate public file field"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("invalid public resource omitted %q: %s", expected, joined)
		}
	}

	duplicate := publicResourceContractManifest()
	second := duplicate.Objects[0]
	second.Key = "second_card"
	duplicate.Objects = append(duplicate.Objects, second)
	state = newValidationState(duplicate, nil)
	state.validateObjects()
	if !strings.Contains(state.errs.Error(), "duplicate public resource key") {
		t.Fatalf("duplicate route key accepted: %v", state.errs)
	}
}

func publicResourceContractManifest() manifestmodel.ManifestSchema {
	fileFields := []definitionmodel.FieldSchema{
		{Key: "file_reference", Type: "text", Required: true},
		{Key: "filename", Type: "text", Required: true},
		{Key: "media_type", Type: "text", Required: true},
		{Key: "byte_size", Type: "integer", Required: true},
		{Key: "content_sha256", Type: "text", Required: true},
		{Key: "archived", Type: "boolean", Required: true},
		{Key: "removed_at", Type: "datetime"},
	}
	file := definitionmodel.ObjectPublicResourceFile{
		FieldKey: "portrait_file", FileIDField: "file_reference", FilenameField: "filename", MediaTypeField: "media_type",
		ByteSizeField: "byte_size", ContentSHA256Field: "content_sha256", DisabledBooleanField: "archived", DisabledTimestampField: "removed_at",
	}
	return manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{
		{
			Key: "card",
			Fields: []definitionmodel.FieldSchema{
				{Key: "share_key", Type: "text", Required: true, Unique: true},
				{Key: "publication_status", Type: "select", Required: true, Validation: definitionmodel.FieldValidation{Options: []string{"draft", "published", "disabled"}}},
				{Key: "full_name", Type: "text", Required: true},
				{Key: "portrait_file", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "source_file"}},
			},
			PublicResources: []definitionmodel.ObjectPublicResource{{
				Key: "digital_business_card", AccessKeyField: "share_key", StateField: "publication_status", ActiveState: "published", Fields: []string{"full_name"}, Files: []definitionmodel.ObjectPublicResourceFile{file},
			}},
		},
		{Key: "source_file", Fields: fileFields},
	}}
}
