package rls

import (
	"context"
	"database/sql"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) WorkspaceRLSSupported() bool { return false }
func (Profile) ApplyWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) error {
	return nil
}
func (Profile) InspectWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) (persistencedriver.WorkspaceRLSStatus, error) {
	return persistencedriver.WorkspaceRLSStatus{}, nil
}
