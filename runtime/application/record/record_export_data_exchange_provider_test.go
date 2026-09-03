package record

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-data-exchange-sdk/modulehost"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordDataExchangeExportUsesStableIDCursor(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	calls := 0
	repository := &exportEdgeRepository{list: func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if query.PageSize == 1 && !query.SkipTotal {
			return recordmodel.RecordPageResult{Total: 201}, nil
		}
		calls++
		if query.Page != 1 || query.PageSize != recordExportBatchSize || !query.SkipTotal || len(query.Sort) != 1 || query.Sort[0].Field != "id" || query.Sort[0].Direction != "asc" {
			t.Fatalf("query=%#v", query)
		}
		if query.AfterID == "" {
			items := make([]recordmodel.Record, recordExportBatchSize)
			for index := range items {
				items[index] = recordmodel.Record{ID: fmt.Sprintf("customer-%03d", index+1), Data: map[string]any{"name": "Acme"}}
			}
			return recordmodel.RecordPageResult{Items: items, HasNext: true}, nil
		}
		if query.AfterID != "customer-200" {
			t.Fatalf("after id=%q", query.AfterID)
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-201", Data: map[string]any{"name": "Last"}}}}, nil
	}}
	exporter := recordExportEdgeService(object, repository)
	providers := NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal {
		return recordExportPrincipal(object.Key)
	})
	providers.Bind(nil, exporter)
	provider, ok := providers.ExportProvider("records")
	if !ok {
		t.Fatal("record export provider unavailable")
	}
	total := 201
	options, _ := json.Marshal(recordDataExchangeExportPayload{ExactTotal: &total})
	request := dataexchange.ExportPageRequest{Scope: dataexchange.Scope{WorkspaceID: "workspace-a", ActorID: "actor"}, ObjectKey: object.Key, Options: options}
	first, err := provider.ReadExportPage(t.Context(), request)
	if err != nil || len(first.Rows) != recordExportBatchSize || first.NextCursor == "" || first.Total != total {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	request.Cursor = first.NextCursor
	second, err := provider.ReadExportPage(t.Context(), request)
	if err != nil || len(second.Rows) != 1 || second.NextCursor != "" || second.Total != total || calls != 2 {
		t.Fatalf("second=%+v calls=%d err=%v", second, calls, err)
	}
	request.Cursor = "record-page:2"
	if _, err = provider.ReadExportPage(t.Context(), request); err == nil {
		t.Fatal("legacy offset cursor was accepted")
	}
}

func TestRecordDataExchangeCompletionPublishesDownloadNotification(t *testing.T) {
	providers := NewDataExchangeProviders(nil)
	occurredAt := time.Date(2026, time.September, 3, 1, 2, 3, 0, time.UTC)
	var published notificationmodel.NotificationIntent
	providers.ConfigureExportNotifications(func(_ context.Context, intent notificationmodel.NotificationIntent) error {
		published = intent
		return nil
	}, func() time.Time { return occurredAt })
	provider, _ := providers.ExportProvider("records")
	completion, ok := provider.(modulehost.ExportCompletionProvider)
	if !ok {
		t.Fatalf("completion capability missing: %T", provider)
	}
	expiresAt := occurredAt.Add(time.Hour)
	if err := completion.CompleteExport(t.Context(), dataexchange.ExportCompletion{
		Scope: dataexchange.Scope{WorkspaceID: "workspace", ActorID: "actor"}, ObjectKey: "customer", JobID: "job-1", Rows: 1200,
		Artifact: dataexchange.Artifact{ID: "artifact-1", Filename: "customer.csv", SHA256: "digest", ExpiresAt: expiresAt},
	}); err != nil {
		t.Fatal(err)
	}
	if published.EventType != "record.export.completed" || published.SubjectType != "record_export" || published.SubjectID != "job-1" || published.ActionState != notificationmodel.NotificationActionOpen || published.RecipientUserIDs[0] != "actor" || published.ExpiresAt != expiresAt.Format(time.RFC3339Nano) || published.OccurredAt != occurredAt.Format(time.RFC3339Nano) || published.Variables["row_count"] != 1200 {
		t.Fatalf("intent=%+v", published)
	}
}
