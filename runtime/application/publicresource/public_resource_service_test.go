package publicresource

import (
	"context"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	publicresourcemodel "github.com/domainry/domainry-runtime/runtime/domain/publicresource/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestGetPublishesOnlyAllowlistedDataAndLiveFilePaths(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "card", PublicResources: []definitionmodel.ObjectPublicResource{{
		Key: "digital_business_card", AccessKeyField: "share_key", StateField: "status", ActiveState: "published", Fields: []string{"full_name", "theme"}, Files: []definitionmodel.ObjectPublicResourceFile{{FieldKey: "portrait_file"}},
	}}}
	repository := &publicResourceRepositoryProbe{projection: publicresourcemodel.Projection{
		WorkspaceID: "workspace-secret", RecordID: "record-secret", Data: map[string]any{"full_name": "Yuki", "theme": "business"}, FileRecords: map[string]string{"portrait_file": "file-secret"},
	}, found: true}
	service := NewService(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}, repository, publicResourceRecordProbe{}, nil, nil)
	resource, err := service.Get(t.Context(), "digital_business_card", "abcdefghijklmnopqrstuvwx")
	if err != nil {
		t.Fatal(err)
	}
	if resource.ResourceKey != "digital_business_card" || resource.Data["full_name"] != "Yuki" || resource.Files["portrait_file"] != "/public-resources/digital_business_card/abcdefghijklmnopqrstuvwx/files/portrait_file" {
		t.Fatalf("resource=%#v", resource)
	}
	if _, leaked := resource.Data["workspace_id"]; leaked {
		t.Fatal("private workspace identity leaked")
	}

	repository.found = false
	if _, err := service.Get(t.Context(), "digital_business_card", "abcdefghijklmnopqrstuvwx"); err != ErrNotFound {
		t.Fatalf("revoked resource error=%v", err)
	}
	if _, err := service.Get(t.Context(), "digital_business_card", "short"); err != ErrRequestInvalid {
		t.Fatalf("short capability key error=%v", err)
	}
}

type publicResourceRepositoryProbe struct {
	projection publicresourcemodel.Projection
	found      bool
	err        error
}

func (probe *publicResourceRepositoryProbe) Find(context.Context, definitionmodel.ObjectSchema, definitionmodel.ObjectPublicResource, string) (publicresourcemodel.Projection, bool, error) {
	return probe.projection, probe.found, probe.err
}

type publicResourceRecordProbe struct{}

func (publicResourceRecordProbe) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{}, nil
}
func (publicResourceRecordProbe) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return recordmodel.Record{}, false, nil
}
func (publicResourceRecordProbe) InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}
func (publicResourceRecordProbe) UpdateRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}
func (publicResourceRecordProbe) UpdateRecordWhere(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
	return false, nil
}
func (publicResourceRecordProbe) DeleteRecord(context.Context, string, definitionmodel.ObjectSchema, string) error {
	return nil
}
func (publicResourceRecordProbe) CommitRecordMutation(context.Context, string, transactionmodel.RecordMutationCommit) error {
	return nil
}
func (publicResourceRecordProbe) CommitRecordMutationBatch(context.Context, string, []transactionmodel.RecordMutationCommit) error {
	return nil
}
func (publicResourceRecordProbe) UniqueExists(context.Context, string, string, string, string, any) (bool, error) {
	return false, nil
}
