package service

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	"testing"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestSchemaSnapshotHashCoversAllManifestDomains(t *testing.T) {
	newSnapshot := func(dictionaries []appschemamodel.DictionarySchema, reports []reportmodel.ReportSchema) appschemamodel.ApplicationSchemaSnapshot {
		snapshot := appschemamodel.ApplicationSchemaSnapshot{TemplateID: "template", TemplateVersion: "1", Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}, Dictionaries: dictionaries, Reports: reports}
		snapshot.SchemaHash = SchemaSnapshotHash(snapshot)
		snapshot.SnapshotVersion = snapshot.SchemaHash
		return snapshot
	}
	first := newSnapshot([]appschemamodel.DictionarySchema{{Key: "status", Items: []appschemamodel.DictionaryItemSchema{{Key: "active", Value: "active"}}}}, nil)
	same := newSnapshot([]appschemamodel.DictionarySchema{{Key: "status", Items: []appschemamodel.DictionaryItemSchema{{Key: "active", Value: "active"}}}}, nil)
	if first.SchemaHash == "" || first.SnapshotVersion != first.SchemaHash || same.SchemaHash != first.SchemaHash {
		t.Fatalf("snapshot version must be stable and explicit: first=%#v same=%#v", first, same)
	}
	dictionaryChanged := newSnapshot([]appschemamodel.DictionarySchema{{Key: "status", Items: []appschemamodel.DictionaryItemSchema{{Key: "inactive", Value: "inactive"}}}}, nil)
	if dictionaryChanged.SchemaHash == first.SchemaHash {
		t.Fatal("dictionary change did not invalidate schema snapshot")
	}
	reportChanged := newSnapshot([]appschemamodel.DictionarySchema{{Key: "status", Items: []appschemamodel.DictionaryItemSchema{{Key: "active", Value: "active"}}}}, []reportmodel.ReportSchema{{Key: "revenue"}})
	if reportChanged.SchemaHash == first.SchemaHash {
		t.Fatal("report change did not invalidate schema snapshot")
	}
}
