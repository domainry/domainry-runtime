package agentdialog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

type agentDialogContextResolverFunc func(context.Context, agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error)

func (fn agentDialogContextResolverFunc) ResolveGlobalContext(ctx context.Context, request agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error) {
	return fn(ctx, request)
}

type agentDialogLimiterFunc func(context.Context, string, int, time.Duration) (ratelimit.Decision, error)

func (fn agentDialogLimiterFunc) Allow(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	return fn(ctx, key, limit, window)
}

func TestAgentDialogRuntimeContextSanitizesAndScopesInput(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1", WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Key: "operator"})
	context := agentDialogRuntimeContext(map[string]any{
		"object_key":       " customer ",
		"record_label":     strings.Repeat("界", 300),
		"selected_records": []any{" one ", 2, "", "two"},
		"filters":          map[string]any{"status": " active ", "unsafe": make(chan int)},
		"reports":          []any{map[string]any{"key": "report-1"}},
		"ignored":          "secret",
	}, principal)
	if context["object_key"] != "customer" || context["workspace_id"] != "workspace-1" || context["user_id"] != "user-1" || context["role"] != "operator" {
		t.Fatalf("runtime context=%#v", context)
	}
	if _, exists := context["ignored"]; exists {
		t.Fatalf("unapproved context key leaked: %#v", context)
	}
	if values := context["selected_records"].([]string); len(values) != 2 || values[0] != "one" || values[1] != "two" {
		t.Fatalf("selected records=%#v", values)
	}
	if label := context["record_label"].(string); len(label) > 512 || !utf8.ValidString(label) {
		t.Fatalf("record label bytes=%d validUTF8=%v", len(label), utf8.ValidString(label))
	}
	message := agentDialogMessageWithRuntimeContext(" hello ", context)
	if !strings.HasPrefix(message, "hello\n\nServer-scoped runtime context:\n") || !strings.Contains(message, `"workspace_id":"workspace-1"`) {
		t.Fatalf("message=%q", message)
	}
	if got := agentDialogMessageWithRuntimeContext(" hello ", map[string]any{"bad": make(chan int)}); got != "hello" {
		t.Fatalf("marshal fallback=%q", got)
	}
}

func TestAgentDialogResolvedContextUsesOnlyServerValidatedProjection(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Key: "operator"})
	var received agentruntime.GlobalAgentContextRequest
	handler := NewAgentDialogHandler(AgentDialogDependencies{
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		ContextResolver: agentDialogContextResolverFunc(func(_ context.Context, request agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error) {
			received = request
			return agentmodel.GlobalAgentContext{
				ContractVersion: agentmodel.GlobalAgentContextContractVersion, ContextRevision: "context-rev", EntrypointKey: "sales-agent", AgentKey: "sales-agent-definition", Surface: "business_workspace", RouteKey: "customers",
				ObjectKey: "customer", RecordID: "customer-1", SelectedRecordIDs: []string{"customer-1"}, Principal: agentmodel.AgentPrincipalReference{UserID: "user-1", RoleKey: "operator", WorkspaceID: "workspace-1", AuthorizationRevision: "auth-rev"},
				AllowedTaskKeys: []string{"customer-review"}, AvailableOperations: []string{"task:customer-review"},
			}, nil
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/run", nil)
	request.Header.Set("X-Agent-Entrypoint-Key", "sales-agent")
	request.Header.Set("X-Surface-Key", "business_workspace")
	request.Header.Set("X-Route-Key", "customers")
	payload, err := handler.agentDialogResolvedUpstreamPayload(request, agentDialogRunRequest{Message: "review", Context: map[string]any{
		"object_key": "customer", "record_id": "customer-1", "selected_record_ids": []any{"customer-1"},
		"available_operation_ids": []any{"task:customer-review", "action:forged"}, "record_label": "untrusted label", "reports": []any{"forged"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if received.EntrypointKey != "sales-agent" || received.RouteKey != "customers" || len(received.AvailableOperationIDs) != 2 {
		t.Fatalf("resolver request=%#v", received)
	}
	metadata := payload["metadata"].(map[string]any)
	runtimeContext := metadata["runtime_context"].(map[string]any)
	if runtimeContext["context_revision"] != "context-rev" || runtimeContext["record_id"] != "customer-1" {
		t.Fatalf("trusted runtime context=%#v", runtimeContext)
	}
	if _, leaked := runtimeContext["record_label"]; leaked {
		t.Fatalf("untrusted hint leaked: %#v", runtimeContext)
	}
	if operations := runtimeContext["available_operations"].([]string); len(operations) != 1 || operations[0] != "task:customer-review" {
		t.Fatalf("validated operations=%v", operations)
	}
}

func TestAgentDialogResolvedContextFailsClosed(t *testing.T) {
	want := errors.New("context denied")
	handler := NewAgentDialogHandler(AgentDialogDependencies{
		Principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
		},
		ContextResolver: agentDialogContextResolverFunc(func(context.Context, agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error) {
			return agentmodel.GlobalAgentContext{}, want
		}),
	})
	if _, err := handler.agentDialogResolvedUpstreamPayload(httptest.NewRequest(http.MethodPost, "/agent-dialog/run", nil), agentDialogRunRequest{}); !errors.Is(err, want) {
		t.Fatalf("resolve error=%v", err)
	}
}

func TestAgentDialogSafeValueHandlesSupportedShapesAndLimits(t *testing.T) {
	if agentDialogSafeString(42) != "" || agentDialogSafeString(" value ") != "value" {
		t.Fatal("safe string normalization mismatch")
	}
	if agentDialogSafeStringList(42, 2) != nil {
		t.Fatal("unsupported list must be rejected")
	}
	if values := agentDialogSafeStringList([]string{" one ", "", "two", "three"}, 2); len(values) != 2 || values[1] != "two" {
		t.Fatalf("typed list=%#v", values)
	}
	if values := agentDialogSafeStringList([]any{"one", "two"}, 1); len(values) != 1 || values[0] != "one" {
		t.Fatalf("untyped limited list=%#v", values)
	}
	items := make([]any, 25)
	for index := range items {
		items[index] = index
	}
	if values := agentDialogSafeValue(items, 2).([]any); len(values) != 20 {
		t.Fatalf("safe list length=%d", len(values))
	}
	if values := agentDialogSafeValue([]any{make(chan int)}, 2).([]any); len(values) != 0 {
		t.Fatalf("unsafe list item was retained: %#v", values)
	}
	if agentDialogSafeValue(nil, 2) != nil || agentDialogSafeValue("value", 0) != nil || agentDialogSafeValue(make(chan int), 2) != nil {
		t.Fatal("unsafe/depth-exhausted value was accepted")
	}
	if agentDialogSafeValue(true, 1) != true || agentDialogSafeValue(float64(2), 1) != float64(2) || agentDialogSafeValue(int64(3), 1) != int64(3) {
		t.Fatal("supported scalar was changed")
	}
	if values := agentDialogSafeValue([]string{"one", "two"}, 2).([]string); len(values) != 2 {
		t.Fatalf("safe typed list=%#v", values)
	}
	if value := agentDialogSafeValue(map[string]any{" ": "ignored", "key": " value "}, 2).(map[string]any); len(value) != 1 || value["key"] != "value" {
		t.Fatalf("safe map=%#v", value)
	}
	if agentDialogSafeValue(map[string]any{"nested": map[string]any{"too_deep": "value"}}, 2) != nil {
		t.Fatal("empty safe map should collapse to nil")
	}
	largeMap := map[string]any{}
	for index := 0; index < 35; index++ {
		largeMap[string(rune('a'+index))] = index
	}
	if value := agentDialogSafeValue(largeMap, 2).(map[string]any); len(value) != 30 {
		t.Fatalf("safe map limit=%d", len(value))
	}
}

func TestAgentDialogWritePolicyMatrixAndExecutionIdentity(t *testing.T) {
	tests := []struct {
		action   string
		metadata map[string]any
		want     string
	}{
		{action: "create_proposal", metadata: map[string]any{"agent_mode": "domain-flow"}},
		{action: "create_proposal", metadata: map[string]any{"run_mode": "read_only"}, want: "agent_dialog.policy_read_only"},
		{action: "create_proposal", metadata: map[string]any{"run_mode": "direct_write"}, want: "agent_dialog.policy_run_mode_denied"},
		{action: "approve_proposal", metadata: map[string]any{"run_mode": "read_only"}, want: "agent_dialog.policy_read_only"},
		{action: "reject_proposal", metadata: map[string]any{"run_mode": "direct_write"}, want: "agent_dialog.policy_run_mode_denied"},
		{action: "approve_proposal", metadata: map[string]any{"run_mode": "suggested_write", "risk_level": "critical"}, want: "agent_dialog.policy_risk_denied"},
		{action: "reject_proposal", metadata: map[string]any{"run_mode": "suggested_write", "risk_level": "high"}},
		{action: "unknown", metadata: nil, want: "agent_dialog.policy_unknown_action"},
	}
	for _, test := range tests {
		if got := agentDialogWritePolicyError(test.action, test.metadata); got != test.want {
			t.Errorf("action=%q metadata=%#v got=%q want=%q", test.action, test.metadata, got, test.want)
		}
	}
	if agentDialogDefaultRunMode("data-analysis") != "read_only" || agentDialogDefaultRunMode("system-ops") != "read_only" || agentDialogDefaultRunMode("other") != "read_only" {
		t.Fatal("default run mode mismatch")
	}
	if policy := agentDialogPolicyFromMetadata(map[string]any{"agent_mode": 42}); policy.AgentMode != "" || policy.RunMode != "read_only" || policy.RiskLevel != "medium" {
		t.Fatalf("default policy=%#v", policy)
	}
	metadata := map[string]any{}
	agentDialogAttachExecutionIdentity(metadata, " ")
	if metadata["requesting_user"] != "anonymous" || metadata["service_role"] != "agent_service_user" || metadata["automation_user"] != "agent_automation" {
		t.Fatalf("execution identity=%#v", metadata)
	}
	agentDialogAttachExecutionIdentity(nil, "user")
}

func TestAgentDialogEnforcesWritePolicyAndAuditsDenial(t *testing.T) {
	audits := 0
	code := ""
	handler := NewAgentDialogHandler(AgentDialogDependencies{
		SecurityAudit: func(*http.Request, string, string, map[string]any) { audits++ },
		WriteError: func(_ http.ResponseWriter, _ *http.Request, status int, errorCode string, _ ...string) {
			if status != http.StatusForbidden {
				t.Fatalf("status=%d", status)
			}
			code = errorCode
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals", nil)
	if handler.enforceAgentDialogWritePolicy(httptest.NewRecorder(), request, "create_proposal", map[string]any{"run_mode": "read_only"}) || audits != 1 || code != "agent_dialog.policy_read_only" {
		t.Fatalf("denial audits=%d code=%q", audits, code)
	}
	if !handler.enforceAgentDialogWritePolicy(httptest.NewRecorder(), request, "create_proposal", map[string]any{"run_mode": "suggested_write"}) || audits != 1 {
		t.Fatal("allowed proposal was rejected or audited")
	}
}

func TestAgentDialogProposalBindingHelpersEscapeAndFilter(t *testing.T) {
	for value, want := range map[any]bool{true: true, false: false, " YES ": true, "off": false, 1: false} {
		if got := agentDialogTruthy(value); got != want {
			t.Errorf("truthy(%#v)=%v want=%v", value, got, want)
		}
	}
	original := map[string]any{"key": "value"}
	cloned := mapFromAny(original)
	cloned["key"] = "changed"
	if original["key"] != "value" || len(mapFromAny("invalid")) != 0 {
		t.Fatal("map cloning contract failed")
	}
	diff := agentAnalysisProposalDiffHTML(map[string]any{"diff": []any{
		map[string]any{"field": `<status>`, "current": `a"b`, "proposed": "done"},
		map[string]any{"field": ""},
	}})
	if !strings.Contains(diff, "&lt;status&gt;") || strings.Contains(diff, `a"b`) {
		t.Fatalf("diff HTML=%q", diff)
	}
	affected := agentAnalysisProposalAffectedHTML(map[string]any{"affected_records": []any{
		map[string]any{"object_key": "customer", "record_id": "1", "label": "<Alice>"},
		map[string]any{"object_key": "", "record_id": "2"},
		map[string]any{"object_key": "customer", "record_id": ""},
	}})
	if !strings.Contains(affected, "record:customer:1") || !strings.Contains(affected, "&lt;Alice&gt;") || strings.Contains(affected, "record::2") {
		t.Fatalf("affected HTML=%q", affected)
	}
}

func TestAgentDialogRateLimitAllowsRejectsAndFailsClosed(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user", WorkspaceID: "workspace"}}, accessfixture.Bundle{Key: "operator"})
	audits := 0
	errorsWritten := 0
	handler := NewAgentDialogHandler(AgentDialogDependencies{
		Config: Config{RateLimitPerMinute: 1}, Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteError:                func(http.ResponseWriter, *http.Request, int, string, ...string) { errorsWritten++ },
		SecurityAuditForPrincipal: func(*http.Request, principalmodel.Principal, string, string, map[string]any) { audits++ },
	})
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/run", nil)
	request.Header.Set("X-Workspace-ID", "workspace")
	request.Pattern = "POST /agent-dialog/run"
	if !handler.allowAgentDialogRequest(request) || handler.allowAgentDialogRequest(request) || audits != 1 {
		t.Fatalf("memory limiter audits=%d", audits)
	}
	nextCalled := false
	response := httptest.NewRecorder()
	handler.agentDialogRateLimited(func(http.ResponseWriter, *http.Request) { nextCalled = true })(response, request)
	if nextCalled || errorsWritten != 1 || response.Header().Get("Retry-After") != "60" {
		t.Fatalf("middleware next=%v errors=%d headers=%v", nextCalled, errorsWritten, response.Header())
	}

	want := errors.New("limiter unavailable")
	handler.UseRateLimiter(agentDialogLimiterFunc(func(context.Context, string, int, time.Duration) (ratelimit.Decision, error) {
		return ratelimit.Decision{}, want
	}))
	handler.UseRateLimiter(nil)
	if handler.allowAgentDialogRequest(request) {
		t.Fatal("limiter failure must fail closed")
	}

	var capturedLimit int
	allowed := NewAgentDialogHandler(AgentDialogDependencies{
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		RateLimiter: agentDialogLimiterFunc(func(_ context.Context, _ string, limit int, _ time.Duration) (ratelimit.Decision, error) {
			capturedLimit = limit
			return ratelimit.Decision{Allowed: true}, nil
		}),
		WriteError: func(http.ResponseWriter, *http.Request, int, string, ...string) {
			t.Fatal("unexpected rate limit error")
		},
	})
	nextCalled = false
	allowed.agentDialogRateLimited(func(http.ResponseWriter, *http.Request) { nextCalled = true })(httptest.NewRecorder(), request)
	if !nextCalled || capturedLimit != 60 {
		t.Fatalf("allowed middleware next=%v default limit=%d", nextCalled, capturedLimit)
	}
}

func TestAgentDialogRateLimitPublishesCeilingOfFixedWindowRemainder(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user", WorkspaceID: "workspace"}}, accessfixture.Bundle{Key: "operator"})
	handler := NewAgentDialogHandler(AgentDialogDependencies{
		Config: Config{RateLimitPerMinute: 1}, Principal: func(*http.Request) principalmodel.Principal { return principal },
		RateLimiter: agentDialogLimiterFunc(func(context.Context, string, int, time.Duration) (ratelimit.Decision, error) {
			return ratelimit.Decision{Allowed: false, Count: 2, Limit: 1, RetryAfter: 1500 * time.Millisecond}, nil
		}),
		WriteError:                func(w http.ResponseWriter, _ *http.Request, status int, _ string, _ ...string) { w.WriteHeader(status) },
		SecurityAuditForPrincipal: func(*http.Request, principalmodel.Principal, string, string, map[string]any) {},
	})
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/run", nil)
	request.Pattern = "POST /agent-dialog/run"
	response := httptest.NewRecorder()
	handler.agentDialogRateLimited(func(http.ResponseWriter, *http.Request) { t.Fatal("limited request reached handler") })(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "2" {
		t.Fatalf("status=%d retry-after=%q", response.Code, response.Header().Get("Retry-After"))
	}
}
