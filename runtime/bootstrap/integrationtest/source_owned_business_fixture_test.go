package integrationtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	agentmodule "github.com/domainry/domainry-agent/module"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	partymodule "github.com/domainry/domainry-party/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
)

type sourceOwnedIntegrationFixtureHandler struct {
	descriptor runtimeext.HandlerDescriptor
}

func (handler sourceOwnedIntegrationFixtureHandler) Descriptor() runtimeext.HandlerDescriptor {
	return handler.descriptor
}

func (handler sourceOwnedIntegrationFixtureHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, raw json.RawMessage) (json.RawMessage, error) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	if field, expected := sourceOwnedFixturePrecondition(execution.Identity().ActionKey); field != "" {
		identity := execution.Identity()
		result, err := execution.QueryRecords(ctx, runtimeext.RecordQuery{
			Operation: runtimeext.QueryGet,
			ObjectKey: identity.ObjectKey,
			RecordID:  identity.RecordID,
		})
		if err != nil {
			return nil, err
		}
		if len(result.Records) != 1 || result.Records[0].Fields[field] != expected {
			return nil, &runtimeext.BusinessError{Code: "backend.action.precondition_failed"}
		}
	}
	if execution.Identity().ActionKey == "lead.activate_due_candidates" || execution.Identity().ActionKey == "lead.create_daily_review_tasks" {
		fromStatus := "new"
		if execution.Identity().ActionKey == "lead.create_daily_review_tasks" {
			fromStatus = "working"
		}
		result, err := execution.QueryRecords(ctx, runtimeext.RecordQuery{
			Operation: runtimeext.QueryList, ObjectKey: "lead", Filters: []runtimeext.Filter{{Field: "status", Operator: "eq", Value: fromStatus}}, Limit: 100,
		})
		if err != nil {
			return nil, err
		}
		for _, record := range result.Records {
			if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
				Operation: runtimeext.MutationUpdate, ObjectKey: "lead", RecordID: record.ID, Fields: map[string]any{"status": "qualified"},
			}); err != nil {
				return nil, err
			}
		}
		return json.Marshal(map[string]any{"activated_count": len(result.Records)})
	}
	if execution.Identity().ActionKey == "lead.fail_due_candidates" {
		return nil, &runtimeext.BusinessError{Code: "lead.activation_failed"}
	}
	fields := map[string]any{}
	switch execution.Identity().ActionKey {
	case "lead.qualify":
		fields["status"] = "qualified"
	case "lead.convert":
		fields["status"] = "converted"
	case "opportunity.advance_stage":
		fields["stage"] = input["stage"]
	case "opportunity.mark_won":
		fields["stage"] = "won"
	case "opportunity.mark_lost":
		fields["stage"] = "lost"
	case "activity.assign_to_me":
		fields["owner"] = execution.Principal().UserID
	case "activity.start":
		fields["status"] = "in_progress"
	case "activity.complete":
		fields["status"] = "completed"
	case "activity.escalate_overdue":
		fields["status"] = "overdue"
	case "contract.approve":
		fields["status"] = "approved"
	case "contract.sign":
		fields["status"] = "signed"
	case "payment.mark_collected":
		fields["status"] = "collected"
	case "customer.apply_automation_verification":
		fields["verification_status"] = input["verification_status"]
	case "sales_order.initialize_risk":
		fields["risk_status"] = "reviewed"
	case "sales_order.submit":
		fields["status"] = "submitted"
	case "sales_order.approve_and_reserve":
		fields["status"] = "approved"
	case "sales_order.reject":
		fields["status"] = "rejected"
	case "member_booking.cancel":
		fields["label"] = "cancelled-member-booking"
	case "customer.verify_business_license":
		return nil, &runtimeext.BusinessError{Code: "crm.business_license_verification_failed"}
	}
	subjectVersion := ""
	if len(fields) > 0 {
		identity := execution.Identity()
		mutation, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
			Operation: runtimeext.MutationUpdate,
			ObjectKey: identity.ObjectKey,
			RecordID:  identity.RecordID,
			Fields:    fields,
		})
		if err != nil {
			return nil, err
		}
		subjectVersion = mutation.Record.UpdatedAt
	}
	if len(handler.descriptor.NotificationEventTypes) > 0 {
		identity := execution.Identity()
		eventType := handler.descriptor.NotificationEventTypes[0]
		sourceEventID := eventType + ":" + identity.ObjectKey + ":" + identity.RecordID + ":v1:admin"
		if _, err := runtimeext.StageNotification(ctx, execution, runtimeext.NotificationIntent{
			EventType: eventType, SourceEventID: sourceEventID, RecipientUserIDs: []string{"admin"}, Surface: "business_workspace",
			SubjectObjectKey: identity.ObjectKey, SubjectRecordID: identity.RecordID, SubjectVersion: subjectVersion, DedupeKey: sourceEventID, GroupKey: "project_action:" + identity.ObjectKey + ":" + identity.RecordID, Alert: true, OccurredAt: time.Now().UTC(),
		}); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(`{}`), nil
}

func initializedIntegrationRuntimeConfig(cfg config.Config) config.Config {
	if strings.TrimSpace(cfg.IdentityWorkspaceID) == "" {
		cfg.IdentityWorkspaceID = "workspace-primary"
	}
	if strings.TrimSpace(cfg.NotificationTenantID) == "" {
		cfg.NotificationTenantID = "tenant-primary"
	}
	if strings.TrimSpace(cfg.NotificationWorkspaceID) == "" {
		cfg.NotificationWorkspaceID = cfg.IdentityWorkspaceID
	}
	if strings.TrimSpace(cfg.PartyTenantID) == "" {
		cfg.PartyTenantID = "tenant-primary"
	}
	if strings.TrimSpace(cfg.PartyWorkspaceID) == "" {
		cfg.PartyWorkspaceID = cfg.IdentityWorkspaceID
	}
	if strings.TrimSpace(cfg.AuditExportTokenKey) == "" {
		cfg.AuditExportTokenKey = "integrationtest-audit-export-signing-key"
	}
	return cfg
}

func newIntegrationRuntime(t *testing.T, cfg config.Config) *bootstrap.Runtime {
	t.Helper()
	// Header-based principals exist only in this in-process test harness. Runtime
	// production configuration never infers or enables them.
	cfg.RuntimeAllowDevIdentityHeaders = true
	cfg = initializedIntegrationRuntimeConfig(cfg)
	raw, err := os.ReadFile(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	identityBinding := newIntegrationIdentityBinding(t, cfg)
	actions, _ := manifest["actions"].([]any)
	registry := runtimeext.NewBusinessHandlerRegistry()
	hasBusinessHandlers := false
	for _, rawAction := range actions {
		action, _ := rawAction.(map[string]any)
		actionKey, _ := action["key"].(string)
		objectKey, _ := action["object_key"].(string)
		if actionKey == "" || objectKey == "" || !sourceOwnedFixtureActionRequiresHandler(actionKey, action) {
			continue
		}
		hasBusinessHandlers = true
		switch strings.TrimSpace(fmt.Sprint(action["kind"])) {
		case "object_create", "object_operation", "bulk_operation":
		default:
			action["kind"] = "record_operation"
		}
		inputType := "example.com/domainry/integrationtest/actions." + sourceOwnedFixtureActionTypeName(actionKey) + "Input"
		outputType := "example.com/domainry/integrationtest/actions." + sourceOwnedFixtureActionTypeName(actionKey) + "Output"
		inputHash := sourceOwnedFixtureContractHash(actionKey + ":input")
		outputHash := sourceOwnedFixtureContractHash(actionKey + ":output")
		action["input_type"] = inputType
		action["output_type"] = outputType
		action["input_contract_sha256"] = inputHash
		action["output_contract_sha256"] = outputHash
		readOperations := []any{"get"}
		objectOperations := []string{"get", "update"}
		if actionKey == "lead.activate_due_candidates" || actionKey == "lead.create_daily_review_tasks" {
			readOperations = []any{"list"}
			objectOperations = []string{"list", "update"}
		}
		readEffects := []any{map[string]any{
			"object_key": objectKey,
			"fields":     []any{},
			"operations": readOperations,
		}}
		objectCapabilities := []runtimeext.ActionObjectCapability{{
			ObjectKey: objectKey, Operations: objectOperations,
		}}
		notificationEventTypes := []string{}
		if expectedEventType := map[string]string{"lead.qualify": "lead.qualified", "lead.convert": "lead.invalid"}[actionKey]; expectedEventType != "" {
			for _, rawEventType := range func() []any { values, _ := manifest["notification_event_types"].([]any); return values }() {
				eventType, _ := rawEventType.(map[string]any)
				if key, _ := eventType["key"].(string); key == expectedEventType {
					notificationEventTypes = append(notificationEventTypes, key)
				}
			}
		}
		relationObjectKey := ""
		switch {
		case strings.HasPrefix(actionKey, "sales_order."):
			relationObjectKey = "customer_account"
		case actionKey == "member_booking.cancel":
			relationObjectKey = "member_profile"
		}
		if relationObjectKey != "" {
			readEffects = append(readEffects, map[string]any{
				"object_key": relationObjectKey,
				"fields":     []any{},
				"operations": []any{"get"},
			})
			objectCapabilities = append(objectCapabilities, runtimeext.ActionObjectCapability{
				ObjectKey: relationObjectKey, Operations: []string{"get"},
			})
		}
		action["effect_set"] = map[string]any{
			"read": readEffects,
			"write": []any{map[string]any{
				"object_key": objectKey,
				"fields":     []any{},
				"operations": []any{"update"},
			}},
		}
		handler := sourceOwnedIntegrationFixtureHandler{descriptor: runtimeext.HandlerDescriptor{
			ActionKey: actionKey, InputType: inputType, OutputType: outputType,
			InputContractSHA256: inputHash, OutputContractSHA256: outputHash,
			HandlerRevision:        "source-owned-integration-fixture-v1",
			ObjectCapabilities:     objectCapabilities,
			NotificationEventTypes: notificationEventTypes,
		}}
		if err := registry.Register(handler); err != nil {
			t.Fatal(err)
		}
	}
	if !hasBusinessHandlers {
		return bootstrap.NewWithScheduler(t.Context(), cfg, identityBinding, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), partymodule.NewFactory(partymodule.Options{}), schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory(), integrationAgentFactory())
	}
	if strings.TrimSpace(cfg.RuntimeVersion) == "" {
		cfg.RuntimeVersion = "integrationtest-runtime-v1"
	}
	// Connector and Connection definitions are Integration-owned. The legacy
	// business fixture keeps historical integration traces as seed evidence,
	// but must not republish an owner catalog through the Runtime manifest.
	delete(manifest, "integrations")
	registry.Freeze()
	normalized, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ManifestPath = filepath.Join(t.TempDir(), strings.TrimSuffix(filepath.Base(cfg.ManifestPath), ".json")+".source-owned.json")
	if err := os.WriteFile(cfg.ManifestPath, normalized, 0o600); err != nil {
		t.Fatal(err)
	}
	return bootstrap.NewWithBusinessHandlersAndScheduler(t.Context(), cfg, registry, identityBinding, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), partymodule.NewFactory(partymodule.Options{}), schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory(), integrationAgentFactory())
}

func integrationAgentFactory() *agentmodule.Factory {
	return agentmodule.NewFactory(agentmodule.Options{BaseURL: "http://127.0.0.1", APIKey: "integration-test", AgentID: 1})
}

func sourceOwnedFixturePrecondition(actionKey string) (string, any) {
	switch actionKey {
	case "lead.convert":
		return "status", "qualified"
	case "opportunity.mark_won", "opportunity.mark_lost":
		return "stage", "negotiation"
	case "activity.start":
		return "status", "open"
	case "activity.escalate_overdue":
		return "status", "in_progress"
	case "contract.approve":
		return "status", "draft"
	case "contract.sign":
		return "status", "approved"
	case "sales_order.submit":
		return "status", "draft"
	case "sales_order.approve_and_reserve", "sales_order.reject":
		return "status", "submitted"
	default:
		return "", nil
	}
}

func sourceOwnedFixtureActionRequiresHandler(actionKey string, action map[string]any) bool {
	kind, _ := action["kind"].(string)
	switch kind {
	case "object_create", "record_update", "record_delete", "record_restore", "transition_state", "conditional_update":
		return actionKey == "activity.assign_to_me"
	default:
		return true
	}
}

func sourceOwnedFixtureActionTypeName(actionKey string) string {
	var result strings.Builder
	upperNext := true
	for _, current := range actionKey {
		if current == '.' || current == '_' || current == '-' {
			upperNext = true
			continue
		}
		if upperNext {
			current = unicode.ToUpper(current)
			upperNext = false
		}
		result.WriteRune(current)
	}
	return result.String()
}

func sourceOwnedFixtureContractHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
