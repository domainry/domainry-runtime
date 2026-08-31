package appschema

import (
	"context"
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"testing"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestprojection "github.com/domainry/domainry-runtime/runtime/domain/manifest/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestMergeInstalledManifestEnvelopePreservesPublishedMetadataIdentityBindings(t *testing.T) {
	active := manifestmodel.ManifestSchema{TemplateID: "active", Description: "stale", IdentityProfileExtensions: []profilebindingmodel.Binding{{ObjectKey: "published-profile"}}}
	seed := manifestmodel.ManifestSchema{
		ManifestHash:              "manifest-hash",
		SourceBlueprintID:         "blueprint-id",
		TargetAPIContractVersion:  "api-v1",
		TargetAPIContractHash:     "api-hash",
		AuthoringContractVersion:  "authoring-v1",
		AuthoringContractHash:     "authoring-hash",
		Description:               "installed",
		IdentityProfileExtensions: []profilebindingmodel.Binding{{ObjectKey: "installed-profile"}},
		Integrations: connectormodel.IntegrationSchema{Connections: []connectormodel.ConnectionSchema{{
			Key: "primary", ConnectorKey: "file_storage", ProviderKey: "local",
		}}},
	}
	published := []notificationmodel.NotificationTemplate{{Key: "welcome"}}

	merged := manifestprojection.MergeInstalledEnvelope(active, seed, published)
	if merged.TemplateID != "active" {
		t.Fatalf("mutable metadata template ID = %q", merged.TemplateID)
	}
	if merged.ManifestHash != seed.ManifestHash || merged.SourceBlueprintID != seed.SourceBlueprintID || merged.Description != seed.Description {
		t.Fatalf("installed identity not preserved: %#v", merged)
	}
	if len(merged.IdentityProfileExtensions) != 1 || len(merged.Integrations.Connections) != 1 {
		t.Fatalf("installed collections not preserved: %#v", merged)
	}
	if merged.IdentityProfileExtensions[0].ObjectKey != "published-profile" {
		t.Fatalf("installed envelope replaced metadata-authoritative identity binding: %#v", merged.IdentityProfileExtensions)
	}
	if len(merged.NotificationTemplates) != 1 || merged.NotificationTemplates[0].Key != "welcome" {
		t.Fatalf("published templates = %#v", merged.NotificationTemplates)
	}
}

func TestRestoreRuntimeManifestPropagatesEachRepositoryFailure(t *testing.T) {
	failure := errors.New("repository failure")
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test Runtime metadata restoration")
	cases := []struct {
		name          string
		notifications runtimeNotificationRepositoryStub
		metadata      runtimeMetadataRepositoryStub
	}{
		{name: "sync published", notifications: runtimeNotificationRepositoryStub{syncErr: failure}},
		{name: "list published", notifications: runtimeNotificationRepositoryStub{listErr: failure}},
		{name: "ensure metadata", metadata: runtimeMetadataRepositoryStub{ensureErr: failure}},
		{name: "load metadata", metadata: runtimeMetadataRepositoryStub{loadErr: failure}},
		{name: "sync metadata", metadata: runtimeMetadataRepositoryStub{syncErr: failure}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewApplicationSchemaRuntimeRestorationApplicationService(tc.notifications, tc.metadata).Restore(t.Context(), manifestmodel.ManifestSchema{}, scope); !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestRestoreRuntimeManifestKeepsOnlyActivePublishedTemplates(t *testing.T) {
	published := notificationmodel.NotificationTemplate{Key: "published"}
	notifications := runtimeNotificationRepositoryStub{records: []notificationmodel.NotificationTemplateRecord{
		{Status: "active", Published: &published},
		{Status: "disabled", Published: &notificationmodel.NotificationTemplate{Key: "disabled"}},
		{Status: "active"},
	}}
	metadata := runtimeMetadataRepositoryStub{manifest: manifestmodel.ManifestSchema{TemplateID: "active"}}
	manifest, err := NewApplicationSchemaRuntimeRestorationApplicationService(notifications, metadata).Restore(t.Context(), manifestmodel.ManifestSchema{}, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test Runtime metadata restoration"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.NotificationTemplates) != 1 || manifest.NotificationTemplates[0].Key != "published" {
		t.Fatalf("notification templates=%#v", manifest.NotificationTemplates)
	}
}

func TestRestoreRuntimeManifestRejectsIncompleteInstalledActionAuthorization(t *testing.T) {
	installed := manifestmodel.ManifestSchema{
		Actions: []definitionmodel.ActionSchema{{
			Key: "booking.cancel", RequiresPermission: "booking.cancel",
		}},
	}
	persisted := installed
	persisted.Actions = []definitionmodel.ActionSchema{{Key: "booking.cancel", RequiresPermission: "booking.read"}}
	_, err := NewApplicationSchemaRuntimeRestorationApplicationService(
		runtimeNotificationRepositoryStub{},
		runtimeMetadataRepositoryStub{manifest: persisted},
	).Restore(
		t.Context(),
		installed,
		principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test Runtime metadata restoration"),
	)
	if err == nil || !strings.Contains(err.Error(), `requires permission "booking.read", want "booking.cancel"`) {
		t.Fatalf("incomplete Action authorization did not block Runtime readiness: %v", err)
	}
}

type runtimeNotificationRepositoryStub struct {
	syncErr error
	listErr error
	records []notificationmodel.NotificationTemplateRecord
}

func (s runtimeNotificationRepositoryStub) SyncPublished(context.Context, principalmodel.SystemScope, []notificationmodel.NotificationTemplate) error {
	return s.syncErr
}

func (s runtimeNotificationRepositoryStub) List(context.Context, principalmodel.SystemScope) ([]notificationmodel.NotificationTemplateRecord, error) {
	return s.records, s.listErr
}

type runtimeMetadataRepositoryStub struct {
	ensureErr error
	loadErr   error
	syncErr   error
	manifest  manifestmodel.ManifestSchema
}

func (s runtimeMetadataRepositoryStub) SyncManifestProjection(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error {
	return s.ensureErr
}

func (s runtimeMetadataRepositoryStub) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return s.manifest, s.loadErr
}

func (s runtimeMetadataRepositoryStub) SyncManifest(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error {
	return s.syncErr
}
