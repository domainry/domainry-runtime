package service

import (
	"context"
	"encoding/json"
	metadataprojection "github.com/domainry/domainry-runtime/runtime/domain/metadata/projection"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

	"hash/fnv"
	"strings"
	"sync"
	"time"
)

const DictionaryItemsCacheTTL = 5 * time.Second

type dictionaryCacheEntry struct {
	result    metadatamodel.DictionaryItemsResult
	expiresAt time.Time
}

// MetadataDictionaryDomainService resolves localized dictionary entries at runtime.
type MetadataDictionaryDomainService struct {
	mu           sync.Mutex
	dictionaries []metadatamodel.DictionarySchema
	version      int
	cache        map[string]dictionaryCacheEntry
}

func NewMetadataDictionaryDomainService(dictionaries []metadatamodel.DictionarySchema) *MetadataDictionaryDomainService {
	runtime := &MetadataDictionaryDomainService{}
	runtime.Replace(dictionaries)
	return runtime
}

func DictionarySchemaVersion(dictionaries []metadatamodel.DictionarySchema) int {
	payload, _ := json.Marshal(dictionaries)
	hash := fnv.New32a()
	_, _ = hash.Write(payload)
	return int(hash.Sum32() & 0x7fffffff)
}

func (s *MetadataDictionaryDomainService) Replace(dictionaries []metadatamodel.DictionarySchema) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.dictionaries = append([]metadatamodel.DictionarySchema(nil), dictionaries...)
	s.version = DictionarySchemaVersion(dictionaries)
	s.cache = map[string]dictionaryCacheEntry{}
	s.mu.Unlock()
}

func (s *MetadataDictionaryDomainService) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.version++
	s.cache = map[string]dictionaryCacheEntry{}
	s.mu.Unlock()
}

func (s *MetadataDictionaryDomainService) Items(ctx context.Context, repository metadatarepository.MetadataRepository, dictionaryKey, locale string, principal principalmodel.Principal) (metadatamodel.DictionaryItemsResult, bool, error) {
	if !principal.Known {
		return metadatamodel.DictionaryItemsResult{}, false, forbidden("auth.permission_denied")
	}
	dictionaryKey, locale = strings.TrimSpace(dictionaryKey), strings.TrimSpace(locale)
	if dictionaryKey == "" {
		return metadatamodel.DictionaryItemsResult{}, false, badRequest("backend.dictionary.missing_key")
	}
	localized := map[string]metadatamodel.LocalizedText{}
	if locale != "" {
		if values, err := repository.ListLocalizedTexts(ctx, principal.WorkspaceID, metadatamodel.LocalizedTextQuery{WorkspaceID: principal.WorkspaceID, EntityType: "dictionary_item", Locale: locale}); err == nil {
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
		result := metadatamodel.DictionaryItemsResult{DictionaryKey: dictionaryKey, Locale: locale, Version: s.version, Items: localizeDictionaryItems(dictionaryKey, dictionaryItemsForLocale(dictionary.Items, locale), localized), CacheTTLMS: DictionaryItemsCacheTTL.Milliseconds()}
		s.cache[cacheKey] = dictionaryCacheEntry{result: cloneDictionaryItemsResult(result), expiresAt: now.Add(DictionaryItemsCacheTTL)}
		return result, false, nil
	}
	return metadatamodel.DictionaryItemsResult{}, false, notFound("backend.dictionary.not_found", "dictionary", dictionaryKey)
}

func localizeDictionaryItems(dictionaryKey string, items []metadatamodel.DictionaryItemSchema, localized map[string]metadatamodel.LocalizedText) []metadatamodel.DictionaryItemSchema {
	out := append([]metadatamodel.DictionaryItemSchema(nil), items...)
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

func dictionaryItemsForLocale(items []metadatamodel.DictionaryItemSchema, locale string) []metadatamodel.DictionaryItemSchema {
	defaults, localized := []metadatamodel.DictionaryItemSchema{}, []metadatamodel.DictionaryItemSchema{}
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
	return metadataprojection.MetadataMergeGeneratedDictionaryItems(defaults, localized)
}

func cloneDictionaryItemsResult(result metadatamodel.DictionaryItemsResult) metadatamodel.DictionaryItemsResult {
	result.Items = append([]metadatamodel.DictionaryItemSchema(nil), result.Items...)
	for index := range result.Items {
		result.Items[index] = metadataprojection.MetadataNormalizeGeneratedDictionaryItem(result.Items[index])
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
