// Dictionary domain service tests.
package service

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
)

type dictionaryRuntimeRepository struct {
	appschemarepository.ApplicationSchemaRepository
	localized []appschemamodel.LocalizedText
}

func (r dictionaryRuntimeRepository) ListLocalizedTexts(context.Context, string, appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error) {
	return append([]appschemamodel.LocalizedText(nil), r.localized...), nil
}

func TestDictionaryRuntimeOwnsStableVersionCacheAndLocalization(t *testing.T) {
	dictionaries := []appschemamodel.DictionarySchema{{Key: "status", Items: []appschemamodel.DictionaryItemSchema{{Key: "active", Value: "active", Label: "Active"}, {Key: "active", Value: "active", Label: "Aktiv", Locale: "de"}}}}
	firstVersion, secondVersion := DictionarySchemaVersion(dictionaries), DictionarySchemaVersion(dictionaries)
	if firstVersion == 0 || firstVersion != secondVersion {
		t.Fatalf("content version drifted: %d vs %d", firstVersion, secondVersion)
	}
	runtime := NewApplicationSchemaDictionaryDomainService(dictionaries)
	repository := dictionaryRuntimeRepository{localized: []appschemamodel.LocalizedText{{EntityKey: "status.active", Property: "description", Locale: "de", Text: "Kann verwendet werden"}}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
	first, cached, err := runtime.Items(t.Context(), repository, "status", "de", principal)
	if err != nil || cached || len(first.Items) != 1 || first.Items[0].Label != "Aktiv" || first.Items[0].Description != "Kann verwendet werden" {
		t.Fatalf("first=%#v cached=%v err=%v", first, cached, err)
	}
	second, cached, err := runtime.Items(t.Context(), repository, "status", "de", principal)
	if err != nil || !cached || !second.Cached || second.Version != first.Version {
		t.Fatalf("second=%#v cached=%v err=%v", second, cached, err)
	}
	runtime.Invalidate()
	third, cached, err := runtime.Items(t.Context(), repository, "status", "de", principal)
	if err != nil || cached || third.Version == first.Version {
		t.Fatalf("third=%#v cached=%v err=%v", third, cached, err)
	}
}
