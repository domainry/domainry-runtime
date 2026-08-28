package metadatamodel

import localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

type DictionarySchema struct {
	Key         string                             `json:"key"`
	Name        string                             `json:"name,omitempty"`
	Description string                             `json:"description,omitempty"`
	I18n        localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Source      string                             `json:"source,omitempty"`
	Items       []DictionaryItemSchema             `json:"items,omitempty"`
	Config      map[string]any                     `json:"config,omitempty"`
}

type DictionaryItemSchema struct {
	Key         string                             `json:"key"`
	Label       string                             `json:"label,omitempty"`
	Description string                             `json:"description,omitempty"`
	I18n        localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Value       string                             `json:"value,omitempty"`
	SortOrder   int                                `json:"sort_order,omitempty"`
	Locale      string                             `json:"locale,omitempty"`
	Status      string                             `json:"status,omitempty"`
	ParentKey   string                             `json:"parent_key,omitempty"`
	Color       string                             `json:"color,omitempty"`
	Icon        string                             `json:"icon,omitempty"`
	Tags        []string                           `json:"tags,omitempty"`
	UI          map[string]any                     `json:"ui,omitempty"`
	Metadata    map[string]any                     `json:"metadata,omitempty"`
	Config      map[string]any                     `json:"config,omitempty"`
}

type DictionaryItemsResult struct {
	DictionaryKey string                 `json:"dictionary_key"`
	Locale        string                 `json:"locale,omitempty"`
	Version       int                    `json:"version"`
	Items         []DictionaryItemSchema `json:"items"`
	Cached        bool                   `json:"cached"`
	CacheTTLMS    int64                  `json:"cache_ttl_ms"`
}
