package projectmodel

import (
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

const ChangedRequiresEmptyDatabaseCode = "backend.metadata.project_model_changed_requires_empty_database"

// RuntimeModel is the validated, in-memory form of model.json used by storage,
// authorization and Workspace initialization. It is not a serializable project
// artifact and deliberately contains no executable behavior definitions.
type RuntimeModel struct {
	SchemaVersion                     string
	ProjectKey                        string
	ProjectName                       string
	DefaultLocale                     string
	TimeZone                          string
	ContentHash                       string
	InitialWorkspaceAdministratorRole string
	Objects                           []definitionmodel.ObjectSchema
	Roles                             []Role
	IdentityProfiles                  []profilebindingmodel.Binding
}

// Compile converts one already decoded project model into Runtime-owned storage
// contracts. Behavior continues to come directly from frozen Go registries.
func Compile(model Model) (RuntimeModel, error) {
	if err := Validate(model, nil); err != nil {
		return RuntimeModel{}, err
	}
	objects, err := RuntimeObjects(model)
	if err != nil {
		return RuntimeModel{}, err
	}
	hash, err := ContentHash(model)
	if err != nil {
		return RuntimeModel{}, fmt.Errorf("hash project model: %w", err)
	}
	runtimeModel := RuntimeModel{
		SchemaVersion: model.SchemaVersion, ProjectKey: model.Project.Key,
		ProjectName: model.Project.Name, DefaultLocale: model.Project.DefaultLocale,
		TimeZone: model.Project.TimeZone, ContentHash: hash,
		InitialWorkspaceAdministratorRole: model.Project.InitialWorkspaceAdministratorRole,
		Objects:                           objects,
	}
	roleKeys := make([]string, 0, len(model.Roles))
	for key := range model.Roles {
		roleKeys = append(roleKeys, key)
	}
	sort.Strings(roleKeys)
	for _, key := range roleKeys {
		runtimeModel.Roles = append(runtimeModel.Roles, model.Roles[key])
	}
	profileKeys := make([]string, 0, len(model.IdentityProfiles))
	for key := range model.IdentityProfiles {
		profileKeys = append(profileKeys, key)
	}
	sort.Strings(profileKeys)
	for _, key := range profileKeys {
		runtimeModel.IdentityProfiles = append(runtimeModel.IdentityProfiles, model.IdentityProfiles[key])
	}
	return runtimeModel, runtimeModel.ValidateTimeZone()
}

func (model RuntimeModel) EffectiveTimeZone() string {
	if strings.TrimSpace(model.TimeZone) == "" {
		return "UTC"
	}
	return model.TimeZone
}

func (model RuntimeModel) ValidateTimeZone() error {
	zone := model.EffectiveTimeZone()
	if zone == "Local" || zone != strings.TrimSpace(zone) || zone != "UTC" && !strings.Contains(zone, "/") {
		return fmt.Errorf("time_zone %q must be a canonical IANA name such as Asia/Tokyo or UTC", zone)
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return fmt.Errorf("cannot load time_zone %q: %w", zone, err)
	}
	return nil
}
