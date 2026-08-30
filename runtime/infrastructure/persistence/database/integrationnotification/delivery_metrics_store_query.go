package integrationnotification

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (r DeliveryMetricsStore) DeliveryMetrics(ctx context.Context, workspaceID, since string) (notificationmodel.NotificationDeliveryMetrics, error) {
	workspaceID, err := requireDeliveryMetricsWorkspaceID(workspaceID)
	if err != nil {
		return notificationmodel.NotificationDeliveryMetrics{}, err
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_outbox_messages", workspaceID).
		Columns("status", "payload_json", "error").Where(ormbuilder.And(
		ormbuilder.GreaterThanOrEqual("created_at", since),
	)).Build()
	if err != nil {
		return notificationmodel.NotificationDeliveryMetrics{}, err
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return notificationmodel.NotificationDeliveryMetrics{}, err
	}
	defer rows.Close()
	result := notificationmodel.NotificationDeliveryMetrics{Since: since, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Summary: notificationmodel.NotificationDeliveryMetricBucket{Key: "all"}}
	channels, templates := map[string]*notificationmodel.NotificationDeliveryMetricBucket{}, map[string]*notificationmodel.NotificationDeliveryMetricBucket{}
	failures := map[string]int{}
	for rows.Next() {
		var status, raw, errorText string
		if err := rows.Scan(&status, &raw, &errorText); err != nil {
			return result, err
		}
		payload := map[string]any{}
		if json.Unmarshal([]byte(raw), &payload) != nil {
			continue
		}
		templateKey, _ := payload["template_key"].(string)
		if strings.TrimSpace(templateKey) == "" {
			continue
		}
		channel, _ := payload["notification_channel"].(string)
		channel = strings.TrimSpace(channel)
		if channel == "" {
			channel = "unknown"
		}
		if channels[channel] == nil {
			channels[channel] = &notificationmodel.NotificationDeliveryMetricBucket{Key: channel}
		}
		if templates[templateKey] == nil {
			templates[templateKey] = &notificationmodel.NotificationDeliveryMetricBucket{Key: templateKey}
		}
		fallback := payload["notification_fallback_root_id"] != nil
		for _, bucket := range []*notificationmodel.NotificationDeliveryMetricBucket{&result.Summary, channels[channel], templates[templateKey]} {
			addDeliveryMetric(bucket, status, fallback)
		}
		if (status == "failed" || status == "dead_letter") && strings.TrimSpace(errorText) != "" {
			failures[strings.TrimSpace(errorText)]++
		}
	}
	for _, bucket := range channels {
		result.ByChannel = append(result.ByChannel, *bucket)
	}
	for _, bucket := range templates {
		result.ByTemplate = append(result.ByTemplate, *bucket)
	}
	sort.Slice(result.ByChannel, func(i, j int) bool { return result.ByChannel[i].Key < result.ByChannel[j].Key })
	sort.Slice(result.ByTemplate, func(i, j int) bool {
		if result.ByTemplate[i].Total == result.ByTemplate[j].Total {
			return result.ByTemplate[i].Key < result.ByTemplate[j].Key
		}
		return result.ByTemplate[i].Total > result.ByTemplate[j].Total
	})
	for errorText, count := range failures {
		result.Failures = append(result.Failures, notificationmodel.NotificationDeliveryFailureMetric{Error: errorText, Count: count})
	}
	sort.Slice(result.Failures, func(i, j int) bool {
		if result.Failures[i].Count == result.Failures[j].Count {
			return result.Failures[i].Error < result.Failures[j].Error
		}
		return result.Failures[i].Count > result.Failures[j].Count
	})
	if len(result.Failures) > 10 {
		result.Failures = result.Failures[:10]
	}
	return result, rows.Err()
}

func requireDeliveryMetricsWorkspaceID(value string) (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", fmt.Errorf("Integration delivery metrics workspace: %w", err)
	}
	return workspaceID.String(), nil
}

func addDeliveryMetric(bucket *notificationmodel.NotificationDeliveryMetricBucket, status string, fallback bool) {
	bucket.Total++
	if fallback {
		bucket.Fallbacks++
	}
	switch strings.TrimSpace(status) {
	case "queued", "sending":
		bucket.Queued++
	case "sent":
		bucket.Sent++
	case "delivered":
		bucket.Delivered++
	case "read":
		bucket.Read++
	case "failed":
		bucket.Failed++
	case "dead_letter":
		bucket.DeadLetter++
	}
}
