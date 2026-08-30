package service

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type dictionaryFailureRepository struct {
	dictionaryRuntimeRepository
	err error
}

func (r dictionaryFailureRepository) ListLocalizedTexts(context.Context, string, appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error) {
	if r.err != nil {
		return nil, r.err
	}
	return append([]appschemamodel.LocalizedText(nil), r.localized...), nil
}

func TestDictionaryServiceRemainingValidationCacheAndLocalization(t *testing.T) {
	var absent *ApplicationSchemaDictionaryDomainService
	absent.Replace(nil)
	absent.Invalidate()
	service := NewApplicationSchemaDictionaryDomainService([]appschemamodel.DictionarySchema{{Key: "status", Items: []appschemamodel.DictionaryItemSchema{
		{Key: "active", Value: "active", Label: "Active"},
		{Key: "active", Value: "active", Label: "Aktiv", Locale: "de"},
	}}})
	if _, _, err := service.Items(t.Context(), dictionaryFailureRepository{}, "status", "", principalmodel.Principal{}); err == nil {
		t.Fatal("unknown principal accepted")
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}
	if _, _, err := service.Items(t.Context(), dictionaryFailureRepository{}, " ", "", principal); err == nil {
		t.Fatal("empty dictionary key accepted")
	}
	repository := dictionaryFailureRepository{dictionaryRuntimeRepository: dictionaryRuntimeRepository{localized: []appschemamodel.LocalizedText{
		{EntityKey: "status.active", Property: "label", Text: "Aktiv lokalisiert"},
		{EntityKey: "other.active", Property: "label", Text: "Ignored"},
	}}}
	result, cached, err := service.Items(t.Context(), repository, "status", "de", principal)
	if err != nil || cached || result.Items[0].Label != "Aktiv lokalisiert" {
		t.Fatalf("localized result=%#v cached=%v err=%v", result, cached, err)
	}
	service.cache["status\x00de"] = dictionaryCacheEntry{result: result, expiresAt: time.Now().Add(-time.Second)}
	if _, cached, err := service.Items(t.Context(), repository, "status", "de", principal); err != nil || cached {
		t.Fatalf("expired cache cached=%v err=%v", cached, err)
	}
	service.cache["status\x00de"] = dictionaryCacheEntry{result: result, expiresAt: time.Now().Add(time.Hour)}
	service.cache["status\x00de"] = dictionaryCacheEntry{result: appschemamodel.DictionaryItemsResult{Version: service.version - 1}, expiresAt: time.Now().Add(time.Hour)}
	if _, cached, err := service.Items(t.Context(), repository, "status", "de", principal); err != nil || cached {
		t.Fatalf("stale-version cache cached=%v err=%v", cached, err)
	}
	if _, _, err := service.Items(t.Context(), dictionaryFailureRepository{err: errors.New("localization failed")}, "missing", "de", principal); err == nil {
		t.Fatal("missing dictionary accepted")
	}
}

func TestDictionaryLocaleAndTextHelperRemainingEdges(t *testing.T) {
	items := []appschemamodel.DictionaryItemSchema{{Key: "only-fr", Locale: "fr"}}
	if got := dictionaryItemsForLocale(items, "de"); len(got) != 1 || got[0].Key != "only-fr" {
		t.Fatalf("fallback items=%#v", got)
	}
	if got := dictionaryItemsForLocale([]appschemamodel.DictionaryItemSchema{{Key: "default"}}, ""); len(got) != 1 {
		t.Fatalf("default items=%#v", got)
	}
	localized := localizeDictionaryItems("status", []appschemamodel.DictionaryItemSchema{{Value: "active"}}, map[string]appschemamodel.LocalizedText{
		"status.active\x00label":       {Text: "Active localized"},
		"status.active\x00description": {Text: "Description localized"},
	})
	if localized[0].Label != "Active localized" || localized[0].Description != "Description localized" {
		t.Fatalf("localized=%#v", localized)
	}
	if got := firstNonEmptyDictionaryText(" "); got != "" {
		t.Fatalf("empty text=%q", got)
	}
}
