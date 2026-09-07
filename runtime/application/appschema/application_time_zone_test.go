package appschema

import (
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestMetadataReloadPublishesConfiguredTimeZoneAndRejectsInvalidZone(t *testing.T) {
	repository := &metadataWatcherRepository{manifest: manifestmodel.ManifestSchema{TemplateID: "shop", Version: "1", TimeZone: "Asia/Tokyo"}}
	runtime := &metadataWatcherRuntime{}
	application := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: repository, Runtime: runtime, Workflows: metadataWatcherWorkflowStub{}})
	if err := application.reloadMetadataFromSource(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.zones) != 1 || runtime.zones[0] != "Asia/Tokyo" {
		t.Fatalf("published zones=%v", runtime.zones)
	}
	repository.manifest.TimeZone = "Not/AZone"
	if err := application.reloadMetadataFromSource(t.Context()); err == nil {
		t.Fatal("invalid application time zone published")
	}
	if len(runtime.zones) != 1 {
		t.Fatal("failed reload replaced application zone")
	}
}
