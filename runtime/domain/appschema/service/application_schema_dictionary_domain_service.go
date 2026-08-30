package service

import (
	"context"
	"encoding/json"
	appschemaprojection "github.com/domainry/domainry-runtime/runtime/domain/appschema/projection"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	"hash/fnv"
	"strings"
	"sync"
	"time"
)

const DictionaryItemsCacheTTL = 5 * time.Second

type dictionaryCacheEntry struct {
	result    appschemamodel.DictionaryItemsResult
	expiresAt time.Time
}

// ApplicationSchemaDictionaryDomainService resolves localized dictionary entries at runtime.
type ApplicationSchemaDictionaryDomainService struct {
	mu           sync.Mutex
	dictionaries []appschemamodel.DictionarySchema
	version      int
	cache        map[string]dictionaryCacheEntry
}

func NewApplicationSchemaDictionaryDomainService(dictionaries []appschemamodel.DictionarySchema) *ApplicationSchemaDictionaryDomainService {
	runtime := &ApplicationSchemaDictionaryDomainService{}
	runtime.Replace(dictionaries)
	return runtime
}

func DictionarySchemaVersion(dictionaries []appschemamodel.DictionarySchema) int {
	payload, _ := json.Marshal(dictionaries)
	hash := fnv.New32a()
	_, _ = hash.Write(payload)
	return int(hash.Sum32() & 0x7fffffff)
}

func (s *ApplicationSchemaDictionaryDomainService) Replace(dictionaries []appschemamodel.DictionarySchema) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.dictionaries = append([]appschemamodel.DictionarySchema(nil), dictionaries...)
	s.version = DictionarySchemaVersion(dictionaries)
	s.cache = map[string]dictionaryCacheEntry{}
	s.mu.Unlock()
}

func (s *ApplicationSchemaDictionaryDomainService) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.version++
	s.cache = map[string]dictionaryCacheEntry{}
	s.mu.Unlock()
}

func (s *ApplicationSchemaDictionaryDomainService) Items(ctx context.Context, repository appschemarepository.ApplicationSchemaRepository, dictionaryKey, locale string, principal principalmodel.Principal) (appschemamodel.DictionaryItemsResult, bool, error) {
	if !principal.Known {
		return appschemamodel.DictionaryItemsResult{}, false, forbidden("auth.permission_denied")
	}
	dictionaryKey, locale = strings.TrimSpace(dictionaryKey), strings.TrimSpace(locale)
	if dictionaryKey == "" {
		return appschemamodel.DictionaryItemsResult{}, false, badRequest("backend.dictionary.missing_key")
	}
	localized := map[string]appschemamodel.LocalizedText{}
	if locale != "" {
		if values, err := repository.ListLocalizedTexts(ctx, principal.WorkspaceID, appschemamodel.LocalizedTextQuery{WorkspaceID: principal.WorkspaceID, EntityType: "dictionary_item", Locale: locale}); err == nil {
			for _, value := range values {
				if strings.HasPrefix(value.EntityKey, dictionaryKey+".") {
					localized[value.EntityKey+"\x00"+value.Property] = value
				}
			}
		}
	}
	now, cacheKey := time.Now(), dictionaryKey+"\x00"+locale
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.cache[cacheKey]; ok && now.Before(cached.expiresAt) && cached.result.Version == s.version {
		result := cloneDictionaryItemsResult(cached.result)
		result.Cached = true
		return result, true, nil
	}
	for _, dictionary := range s.dictionaries {
		if strings.TrimSpace(dictionary.Key) != dictionaryKey {
			continue
		}
		result := appschemamodel.DictionaryItemsResult{DictionaryKey: dictionaryKey, Locale: locale, Version: s.version, Items: localizeDictionaryItems(dictionaryKey, dictionaryItemsForLocale(dictionary.Items, locale), localized), CacheTTLMS: DictionaryItemsCacheTTL.Milliseconds()}
		s.cache[cacheKey] = dictionaryCacheEntry{result: cloneDictionaryItemsResult(result), expiresAt: now.Add(DictionaryItemsCacheTTL)}
		return result, false, nil
	}
	return appschemamodel.DictionaryItemsResult{}, false, notFound("backend.dictionary.not_found", "dictionary", dictionaryKey)
}

func localizeDictionaryItems(dictionaryKey string, items []appschemamodel.DictionaryItemSchema, localized map[string]appschemamodel.LocalizedText) []appschemamodel.DictionaryItemSchema {
	out := append([]appschemamodel.DictionaryItemSchema(nil), items...)
	for index := range out {
		itemKey := strings.Trim(dictionaryKey+"."+firstNonEmptyDictionaryText(out[index].Key, out[index].Value), ".")
		if value := localized[itemKey+"\x00label"]; value.Text != "" {
			out[index].Label = value.Text
		}
		if value := localized[itemKey+"\x00description"]; value.Text != "" {
			out[index].Description = value.Text
		}
	}
	return out
}

func dictionaryItemsForLocale(items []appschemamodel.DictionaryItemSchema, locale string) []appschemamodel.DictionaryItemSchema {
	defaults, localized := []appschemamodel.DictionaryItemSchema{}, []appschemamodel.DictionaryItemSchema{}
	for _, item := range items {
		if strings.TrimSpace(item.Locale) == "" {
			defaults = append(defaults, item)
		} else if locale != "" && strings.EqualFold(item.Locale, locale) {
			localized = append(localized, item)
		}
	}
	if len(defaults) == 0 && len(localized) == 0 {
		defaults = append(defaults, items...)
	}
	return appschemaprojection.ApplicationSchemaMergeGeneratedDictionaryItems(defaults, localized)
}

func cloneDictionaryItemsResult(result appschemamodel.DictionaryItemsResult) appschemamodel.DictionaryItemsResult {
	result.Items = append([]appschemamodel.DictionaryItemSchema(nil), result.Items...)
	for index := range result.Items {
		result.Items[index] = appschemaprojection.ApplicationSchemaNormalizeGeneratedDictionaryItem(result.Items[index])
	}
	return result
}

func firstNonEmptyDictionaryText(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
