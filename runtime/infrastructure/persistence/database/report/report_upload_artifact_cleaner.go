package report

import (
	"context"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type UploadArtifactCleaner struct {
	store   *database.RuntimeStore
	enabled bool
}

func NewUploadArtifactCleaner(store *database.RuntimeStore, objects []definitionmodel.ObjectSchema) *UploadArtifactCleaner {
	enabled := false
	for _, object := range objects {
		if strings.TrimSpace(object.Key) != "download_task" {
			continue
		}
		fields := map[string]bool{}
		for _, field := range object.Fields {
			fields[strings.TrimSpace(field.Key)] = true
		}
		enabled = fields["token_status"] && fields["file_name"] && fields["status"]
	}
	return &UploadArtifactCleaner{store: store, enabled: enabled}
}

func (c *UploadArtifactCleaner) ExpireUploadReferences(ctx context.Context, now time.Time, limit int) (int, error) {
	if c == nil || !c.enabled {
		return 0, nil
	}
	cutoff := now.Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	query, args, buildErr := ormbuilder.NewSelectBuilder(c.store.SQLRenderer, "download_task").Columns("workspace_id", "id").
		Where(ormbuilder.And(ormbuilder.Equal("token_status", "active"), ormbuilder.LessThanOrEqual("updated_at", cutoff))).
		OrderBy(ormbuilder.Ascending("updated_at"), ormbuilder.Ascending("id")).Limit(limit).Build()
	if buildErr != nil {
		return 0, fmt.Errorf("build expired download task query: %w", buildErr)
	}
	rows, err := c.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	type identity struct{ workspaceID, id string }
	identities := []identity{}
	for rows.Next() {
		var item identity
		if err := rows.Scan(&item.workspaceID, &item.id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		identities = append(identities, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()
	expired := 0
	for _, item := range identities {
		update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(c.store.SQLRenderer, "download_task", item.workspaceID).
			Set("token_status", "expired").Set("status", "expired").Set("file_name", "").Set("updated_at", now.UTC().Format(time.RFC3339Nano)).
			Where(ormbuilder.And(ormbuilder.Equal("id", item.id), ormbuilder.Equal("token_status", "active"))).Build()
		if buildErr != nil {
			return expired, fmt.Errorf("build download task expiration: %w", buildErr)
		}
		changed, err := c.store.DB().ExecContext(ctx, update, updateArgs...)
		if err != nil {
			return expired, err
		}
		count, err := changed.RowsAffected()
		if err != nil {
			return expired, fmt.Errorf("read expired download task count: %w", err)
		}
		expired += int(count)
	}
	return expired, nil
}
