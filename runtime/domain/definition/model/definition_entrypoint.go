package definitionmodel

import localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

type EntryPointSchema struct {
	Key                 string                             `json:"key"`
	Name                string                             `json:"name,omitempty"`
	Description         string                             `json:"description,omitempty"`
	I18n                localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Audience            string                             `json:"audience,omitempty"`
	RequiredPermissions []string                           `json:"required_permissions"`
	Default             bool                               `json:"default,omitempty"`
	Config              map[string]any                     `json:"config,omitempty"`
}
