package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strconv"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

const notificationFallbackMaxHops = 5

// enqueueNotificationFallback persists the next precompiled fallback before
// the parent is moved to dead-letter. That ordering closes the crash window:
// after a crash the leased parent is retried with the same deterministic child
// ID, so it cannot fan out duplicate fallback deliveries.
func (s *IntegrationApplicationService) enqueueNotificationFallback(ctx context.Context, parent integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal) error {
	plan := fallbackPlan(parent.Payload["notification_fallback_plan"])
	if len(plan) == 0 {
		return nil
	}
	hop := fallbackInt(parent.Payload["notification_fallback_hop"])
	if hop < 0 || hop >= len(plan) || hop >= notificationFallbackMaxHops {
		return nil
	}
	item := plan[hop]
	payload, ok := item["payload"].(map[string]any)
	if !ok {
		return fmt.Errorf("fallback payload %d is invalid", hop)
	}
	payload = cloneMap(payload)
	rootID := strings.TrimSpace(fmt.Sprint(parent.Payload["notification_fallback_root_id"]))
	if rootID == "" || rootID == "<nil>" {
		rootID = parent.ID
	}
	nextHop := hop + 1
	payload["notification_fallback_plan"] = plan
	payload["notification_fallback_root_id"] = rootID
	payload["notification_fallback_parent_id"] = parent.ID
	payload["notification_fallback_hop"] = nextHop
	sum := sha256.Sum256([]byte(rootID + "\x00" + strconv.Itoa(nextHop)))
	childID := "notification-fallback-" + hex.EncodeToString(sum[:16])
	message := integrationmodel.IntegrationOutboxMessage{
		ID: childID, WorkspaceID: parent.WorkspaceID, ConnectorKey: firstString(item["connector_key"]),
		ConnectionKey: firstString(item["connection_key"]), Operation: firstString(item["operation"]),
		Status: "queued", Payload: payload, EventID: parent.EventID, RequestRef: "notification-fallback:" + rootID + ":" + strconv.Itoa(nextHop), CreatedBy: principal.UserID,
	}
	if deliverAfter := strings.TrimSpace(fmt.Sprint(payload["notification_deliver_after"])); deliverAfter != "" && deliverAfter != "<nil>" {
		message.NextAttemptAt = deliverAfter
	}
	if message.ConnectorKey == "" || message.Operation == "" {
		return fmt.Errorf("fallback target %d is incomplete", hop)
	}
	saved, err := s.publicationRepo.InsertOutbox(ctx, message.WorkspaceID, message)
	if err != nil {
		text := strings.ToLower(err.Error())
		if strings.Contains(text, "unique") || strings.Contains(text, "duplicate") {
			return nil
		}
		return err
	}
	s.audit(ctx, "notification_fallback_enqueued", "integration_outbox", saved.ID, principal, "Enqueued precompiled notification fallback", nil, integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{"root_id": rootID, "parent_id": parent.ID, "hop": nextHop, "connector_key": saved.ConnectorKey})
	s.wakeIntegrationOutbox(saved)
	return nil
}

func fallbackPlan(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(map[string]any); ok {
				result = append(result, value)
			}
		}
		return result
	default:
		return nil
	}
}

func fallbackInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		parsed, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
		return parsed
	}
}
