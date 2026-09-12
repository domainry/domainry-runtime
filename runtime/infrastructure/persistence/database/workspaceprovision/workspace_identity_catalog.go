package workspaceprovision

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// ActiveIdentityWorkspaces is the installation-owned catalog used by embedded
// Identity. The database belongs to one installation; only its initial row
// carries initial_installation_identity. A foreign initial marker is excluded.
func ActiveIdentityWorkspaces(ctx context.Context, store *database.RuntimeStore, reference string) ([]string, error) {
	if _, found, err := LoadInstallation(ctx, store); err != nil {
		return nil, err
	} else if !found {
		return nil, fmt.Errorf("installation unavailable")
	}
	installationID, err := store.InstallationIdentity(ctx)
	if err != nil {
		return nil, err
	}
	predicate := query.And(query.Equal("status", "active"), query.Or(query.IsNull("initial_installation_identity"), query.Equal("initial_installation_identity", installationID)))
	if reference != "" {
		predicate = query.And(predicate, query.Or(query.Equal("id", reference), query.Equal("canonical_code", reference)))
	}
	statement, arguments, err := query.NewSelectBuilder(store.RuntimeRenderer(), "_workspaces").Columns("id").Where(predicate).OrderBy(query.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := store.DB().QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func ResolveActiveIdentityWorkspace(ctx context.Context, store *database.RuntimeStore, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", sql.ErrNoRows
	}
	ids, err := ActiveIdentityWorkspaces(ctx, store, reference)
	if err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", sql.ErrNoRows
	}
	return ids[0], nil
}
