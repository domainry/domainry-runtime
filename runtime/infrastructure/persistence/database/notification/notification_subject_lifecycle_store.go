package notification

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type NotificationSubjectLifecycleStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewNotificationSubjectLifecycleStore(store *database.RuntimeStore) *NotificationSubjectLifecycleStore {
	return &NotificationSubjectLifecycleStore{store: store}
}

func (s *NotificationSubjectLifecycleStore) database() *sql.DB {
	if s.db != nil {
		return s.db
	}
	return s.store.DB()
}

func (s *NotificationSubjectLifecycleStore) Owner(context.Context) string { return "notification" }

func (s *NotificationSubjectLifecycleStore) PreviewSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	counts := map[string]int64{}
	for key, tableColumn := range map[string][2]string{
		"inbox_items": {"notification_inbox_items", "recipient_user_id"}, "preferences": {"notification_recipient_preferences", "recipient_key"},
		"delivery_reservations": {"notification_delivery_reservations", "recipient_key"}, "saved_views": {"notification_inbox_saved_views", "recipient_user_id"},
	} {
		query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier(tableColumn[0]) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier(tableColumn[1]) + " = " + s.store.Placeholder(2)
		var count int64
		if err := s.database().QueryRowContext(ctx, query, workspaceID, identity).Scan(&count); err != nil {
			return nil, err
		}
		counts[key] = count
	}
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("notification_inbox_delegations") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND (" + s.store.Identifier("owner_user_id") + " = " + s.store.Placeholder(2) + " OR " + s.store.Identifier("delegate_user_id") + " = " + s.store.Placeholder(3) + ")"
	var delegationCount int64
	if err := s.database().QueryRowContext(ctx, query, workspaceID, identity, identity).Scan(&delegationCount); err != nil {
		return nil, err
	}
	counts["delegations"] = delegationCount
	return json.Marshal(counts)
}

func (s *NotificationSubjectLifecycleStore) ExportSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	query := "SELECT " + strings.Join(database.QuotedColumns(s.store, []string{"id", "surface", "event_type", "source", "category", "severity", "title", "body", "action_state", "alert_state", "first_occurred_at", "last_occurred_at", "read_at", "archived_at"}), ", ") + " FROM " + s.store.TableIdentifier("notification_inbox_items") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("recipient_user_id") + " = " + s.store.Placeholder(2) + " ORDER BY " + s.store.Identifier("created_at")
	rows, err := s.database().QueryContext(ctx, query, workspaceID, identity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var id, surface, eventType, source, category, severity, title, body, actionState, alertState, first, last, readAt, archivedAt string
		if err := rows.Scan(&id, &surface, &eventType, &source, &category, &severity, &title, &body, &actionState, &alertState, &first, &last, &readAt, &archivedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]string{"id": id, "surface": surface, "event_type": eventType, "source": source, "category": category, "severity": severity, "title": title, "body": body, "action_state": actionState, "alert_state": alertState, "first_occurred_at": first, "last_occurred_at": last, "read_at": readAt, "archived_at": archivedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"inbox_items": items})
}

func (s *NotificationSubjectLifecycleStore) EraseSubject(ctx context.Context, workspaceID, identity string, _ []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	anonymous := notificationAnonymousSubject(workspaceID, identity)
	changed := map[string]int64{}
	if changed["inbox_items"], err = s.anonymizeInboxItems(ctx, tx, workspaceID, identity, anonymous); err != nil {
		return nil, err
	}
	if changed["events"], err = s.anonymizeEventPayloads(ctx, tx, workspaceID, identity, anonymous); err != nil {
		return nil, err
	}
	if changed["channel_plans"], err = s.anonymizeChannelPayloads(ctx, tx, workspaceID, identity, anonymous); err != nil {
		return nil, err
	}
	updates := []struct{ key, table, column string }{
		{"alert_groups", "notification_alert_groups", "recipient_user_id"}, {"delivery_reservations", "notification_delivery_reservations", "recipient_key"},
	}
	for _, update := range updates {
		query := "UPDATE " + s.store.TableIdentifier(update.table) + " SET " + s.store.Identifier(update.column) + " = " + s.store.Placeholder(1) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier(update.column) + " = " + s.store.Placeholder(3)
		result, updateErr := tx.ExecContext(ctx, query, anonymous, workspaceID, identity)
		if updateErr != nil {
			return nil, updateErr
		}
		changed[update.key], _ = result.RowsAffected()
	}
	result, updateErr := tx.ExecContext(ctx, "UPDATE "+s.store.TableIdentifier("notification_alert_groups")+" SET "+s.store.Identifier("acknowledged_by")+" = "+s.store.Placeholder(1)+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(2)+" AND "+s.store.Identifier("acknowledged_by")+" = "+s.store.Placeholder(3), anonymous, workspaceID, identity)
	if updateErr != nil {
		return nil, updateErr
	}
	changed["alert_acknowledgements"], _ = result.RowsAffected()
	for key, tableColumn := range map[string][2]string{"preferences": {"notification_recipient_preferences", "recipient_key"}, "saved_views": {"notification_inbox_saved_views", "recipient_user_id"}, "delegations": {"notification_inbox_delegations", "owner_user_id"}} {
		condition := s.store.Identifier(tableColumn[1]) + " = " + s.store.Placeholder(2)
		args := []any{workspaceID, identity}
		if key == "delegations" {
			condition = "(" + condition + " OR " + s.store.Identifier("delegate_user_id") + " = " + s.store.Placeholder(3) + ")"
			args = append(args, identity)
		}
		result, deleteErr := tx.ExecContext(ctx, "DELETE FROM "+s.store.TableIdentifier(tableColumn[0])+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(1)+" AND "+condition, args...)
		if deleteErr != nil {
			return nil, deleteErr
		}
		changed[key], _ = result.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"anonymous_subject": anonymous, "changed": changed, "content_redacted": true, "at": time.Now().UTC()})
}

func (s *NotificationSubjectLifecycleStore) anonymizeInboxItems(ctx context.Context, tx *sql.Tx, workspaceID, identity, anonymous string) (int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+s.store.Identifier("id")+", "+s.store.Identifier("payload_json")+" FROM "+s.store.TableIdentifier("notification_inbox_items")+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(1)+" AND "+s.store.Identifier("recipient_user_id")+" = "+s.store.Placeholder(2), workspaceID, identity)
	if err != nil {
		return 0, err
	}
	type itemPayload struct{ id, raw string }
	values := []itemPayload{}
	for rows.Next() {
		var value itemPayload
		if err := rows.Scan(&value.id, &value.raw); err != nil {
			rows.Close()
			return 0, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, value := range values {
		var item notificationmodel.NotificationInboxItem
		if err := json.Unmarshal([]byte(value.raw), &item); err != nil {
			return 0, err
		}
		item.RecipientUserID, item.Title, item.Body, item.Facts, item.Actions = anonymous, "[erased]", "[erased]", nil, nil
		item.SubjectID, item.SubjectVersion, item.TemplateContentHash = "", "", ""
		raw, _ := json.Marshal(item)
		query := "UPDATE " + s.store.TableIdentifier("notification_inbox_items") + " SET " + s.store.Identifier("recipient_user_id") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("title") + " = '[erased]', " + s.store.Identifier("body") + " = '[erased]', " + s.store.Identifier("search_text") + " = '', " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("subject_id") + " = '' WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(4)
		if _, err := tx.ExecContext(ctx, query, anonymous, string(raw), workspaceID, value.id); err != nil {
			return 0, err
		}
	}
	return int64(len(values)), nil
}

func (s *NotificationSubjectLifecycleStore) anonymizeEventPayloads(ctx context.Context, tx *sql.Tx, workspaceID, identity, anonymous string) (int64, error) {
	return s.rewritePayloads(ctx, tx, "notification_events", workspaceID, func(raw []byte) ([]byte, bool, error) {
		var event notificationmodel.NotificationEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, false, err
		}
		if !replaceNotificationRecipient(event.RecipientUserIDs, identity, anonymous) {
			return raw, false, nil
		}
		event.Snapshot.Title, event.Snapshot.Body, event.Snapshot.Facts, event.Snapshot.Actions = "[erased]", "[erased]", nil, nil
		event.LocalizedSnapshots, event.CorrelationID, event.TraceID = nil, "", ""
		for index := range event.ChannelPlans {
			replaceNotificationRecipient(event.ChannelPlans[index].RecipientUserIDs, identity, anonymous)
			event.ChannelPlans[index].Variables = nil
		}
		value, err := json.Marshal(event)
		return value, true, err
	})
}

func (s *NotificationSubjectLifecycleStore) anonymizeChannelPayloads(ctx context.Context, tx *sql.Tx, workspaceID, identity, anonymous string) (int64, error) {
	return s.rewritePayloads(ctx, tx, "notification_channel_plans", workspaceID, func(raw []byte) ([]byte, bool, error) {
		var plan notificationmodel.NotificationChannelPlan
		if err := json.Unmarshal(raw, &plan); err != nil {
			return nil, false, err
		}
		if !replaceNotificationRecipient(plan.RecipientUserIDs, identity, anonymous) {
			return raw, false, nil
		}
		plan.Variables, plan.DigestItemTitle, plan.DigestItemBody = nil, "[erased]", "[erased]"
		value, err := json.Marshal(plan)
		return value, true, err
	})
}

func (s *NotificationSubjectLifecycleStore) rewritePayloads(ctx context.Context, tx *sql.Tx, table, workspaceID string, rewrite func([]byte) ([]byte, bool, error)) (int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+s.store.Identifier("id")+", "+s.store.Identifier("payload_json")+" FROM "+s.store.TableIdentifier(table)+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(1), workspaceID)
	if err != nil {
		return 0, err
	}
	values := [][2]string{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		values = append(values, [2]string{id, raw})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	var changed int64
	for _, value := range values {
		raw, matched, rewriteErr := rewrite([]byte(value[1]))
		if rewriteErr != nil {
			return changed, rewriteErr
		}
		if !matched {
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE "+s.store.TableIdentifier(table)+" SET "+s.store.Identifier("payload_json")+" = "+s.store.Placeholder(1)+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(2)+" AND "+s.store.Identifier("id")+" = "+s.store.Placeholder(3), string(raw), workspaceID, value[0]); err != nil {
			return changed, fmt.Errorf("rewrite %s payload: %w", table, err)
		}
		changed++
	}
	return changed, nil
}

func replaceNotificationRecipient(values []string, identity, anonymous string) bool {
	matched := false
	for index := range values {
		if values[index] == identity {
			values[index], matched = anonymous, true
		}
	}
	return matched
}

func notificationAnonymousSubject(workspaceID, identity string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + identity))
	return "erased-" + hex.EncodeToString(sum[:12])
}
