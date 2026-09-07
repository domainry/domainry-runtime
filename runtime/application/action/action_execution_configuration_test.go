package action

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestActionCapturesZoneAndRevisionFromOneConfigurationRead(t *testing.T) {
	current := ApplicationExecutionConfiguration{SchemaRevision: "catalog-1", TimeZone: "Asia/Tokyo"}
	reads := 0
	executor := NewBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
		RuntimeRevision: "runtime", ProjectRevision: "project",
		ResolveMetadataRevision: func(context.Context, principalmodel.Principal) (string, error) {
			t.Fatal("configuration was split into a second revision read")
			return "", errors.New("unexpected")
		},
		ResolveApplicationConfiguration: func(context.Context, principalmodel.Principal) (ApplicationExecutionConfiguration, error) {
			reads++
			return current, nil
		},
	})
	if failures := executor.ValidationErrors(); len(failures) != 0 {
		t.Fatal(failures)
	}
	identity, zone, err := executor.executionConfiguration(t.Context(), actionmodel.ActionInvocation{}, definitionmodel.ActionSchema{Key: "sale.settle"}, runtimeext.HandlerDescriptor{}, "execution-1")
	if err != nil || reads != 1 || identity.ApplicationSchemaRevision != "catalog-1" || zone != "Asia/Tokyo" {
		t.Fatalf("capture=%#v zone=%q reads=%d error=%v", identity, zone, reads, err)
	}
	session := &businessActionExecution{identity: identity, applicationTimeZone: zone}
	current = ApplicationExecutionConfiguration{SchemaRevision: "catalog-2", TimeZone: "America/New_York"}
	for range 2 {
		got, err := session.ApplicationTimeZone()
		if err != nil || got != "Asia/Tokyo" || session.Identity().ApplicationSchemaRevision != "catalog-1" || reads != 1 {
			t.Fatal("one running Action observed a later configuration")
		}
	}
	nextIdentity, nextZone, err := executor.executionConfiguration(t.Context(), actionmodel.ActionInvocation{}, definitionmodel.ActionSchema{Key: "sale.settle"}, runtimeext.HandlerDescriptor{}, "execution-2")
	if err != nil || nextIdentity.ApplicationSchemaRevision != "catalog-2" || nextZone != "America/New_York" || reads != 2 {
		t.Fatal("a later Action did not observe the new configuration")
	}
}

func TestActionConfigurationFailsBeforeHandlerForInvalidRequiredPolicy(t *testing.T) {
	for _, value := range []ApplicationExecutionConfiguration{{SchemaRevision: "r"}, {SchemaRevision: "r", TimeZone: "Local"}, {TimeZone: "Asia/Tokyo"}} {
		executor := NewBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
			RuntimeRevision: "runtime", ProjectRevision: "project",
			ResolveApplicationConfiguration: func(context.Context, principalmodel.Principal) (ApplicationExecutionConfiguration, error) {
				return value, nil
			},
		})
		if _, _, err := executor.executionConfiguration(t.Context(), actionmodel.ActionInvocation{}, definitionmodel.ActionSchema{}, runtimeext.HandlerDescriptor{}, "execution"); err == nil {
			t.Fatalf("accepted %#v", value)
		}
	}
	executor := NewBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{RuntimeRevision: "runtime", ProjectRevision: "project", ApplicationSchemaRevision: "r"})
	if _, zone, err := executor.executionConfiguration(t.Context(), actionmodel.ActionInvocation{}, definitionmodel.ActionSchema{}, runtimeext.HandlerDescriptor{}, "legacy"); err != nil || zone != "" {
		t.Fatalf("legacy nonconsumer changed: %q %v", zone, err)
	}
	if _, err := (&businessActionExecution{}).ApplicationTimeZone(); err == nil {
		t.Fatal("legacy consumer silently chose UTC")
	}
}
