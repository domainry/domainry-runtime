package principalmodel

import (
	"errors"
	"strings"
)

var ErrWorkspaceIDRequired = errors.New("workspace id is required")
var ErrWorkspaceIDReserved = errors.New("default workspace id is reserved")
var ErrSystemScopeRequired = errors.New("valid system scope is required")
var ErrPrincipalScopeRequired = errors.New("authenticated principal scope is required")

// InstallationWorkspaceID is populated from the durable tenant installation
// marker before Runtime assembles any tenant-bound service or worker.
var InstallationWorkspaceID string

func ConfigureInstallationWorkspaceID(value string) error {
	id, err := NewWorkspaceID(value)
	if err != nil {
		return err
	}
	InstallationWorkspaceID = id.String()
	return nil
}

// WorkspaceID is a non-empty tenant boundary. Its value is intentionally
// private so Command/Query scope construction cannot bypass validation.
type WorkspaceID struct{ value string }

func NewWorkspaceID(value string) (WorkspaceID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return WorkspaceID{}, ErrWorkspaceIDRequired
	}
	if strings.EqualFold(value, "default") {
		return WorkspaceID{}, ErrWorkspaceIDReserved
	}
	return WorkspaceID{value: value}, nil
}

func (id WorkspaceID) String() string { return id.value }
func (id WorkspaceID) Valid() bool    { return id.value != "" }

type CommandScope struct {
	workspace WorkspaceID
	system    SystemScope
}

func NewWorkspaceCommandScope(workspaceID string) (CommandScope, error) {
	id, err := NewWorkspaceID(workspaceID)
	if err != nil {
		return CommandScope{}, err
	}
	return CommandScope{workspace: id}, nil
}

func NewSystemCommandScope(system SystemScope) (CommandScope, error) {
	if !system.Valid() {
		return CommandScope{}, ErrSystemScopeRequired
	}
	return CommandScope{system: system}, nil
}

func (scope CommandScope) WorkspaceID() WorkspaceID { return scope.workspace }
func (scope CommandScope) SystemScope() SystemScope { return scope.system }
func (scope CommandScope) Valid() bool              { return scope.workspace.Valid() != scope.system.Valid() }

type QueryScope struct {
	workspace WorkspaceID
	system    SystemScope
}

func NewWorkspaceQueryScope(workspaceID string) (QueryScope, error) {
	id, err := NewWorkspaceID(workspaceID)
	if err != nil {
		return QueryScope{}, err
	}
	return QueryScope{workspace: id}, nil
}

func NewSystemQueryScope(system SystemScope) (QueryScope, error) {
	if !system.Valid() {
		return QueryScope{}, ErrSystemScopeRequired
	}
	return QueryScope{system: system}, nil
}

func (scope QueryScope) WorkspaceID() WorkspaceID { return scope.workspace }
func (scope QueryScope) SystemScope() SystemScope { return scope.system }
func (scope QueryScope) Valid() bool              { return scope.workspace.Valid() != scope.system.Valid() }

func CommandScopeForPrincipal(principal Principal) (CommandScope, error) {
	if !principal.Known {
		return CommandScope{}, ErrPrincipalScopeRequired
	}
	if principal.SystemScope.Valid() {
		return NewSystemCommandScope(principal.SystemScope)
	}
	return NewWorkspaceCommandScope(principal.WorkspaceID)
}

func QueryScopeForPrincipal(principal Principal) (QueryScope, error) {
	if !principal.Known {
		return QueryScope{}, ErrPrincipalScopeRequired
	}
	if principal.SystemScope.Valid() {
		return NewSystemQueryScope(principal.SystemScope)
	}
	return NewWorkspaceQueryScope(principal.WorkspaceID)
}
