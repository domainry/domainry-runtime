package runtimehost

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestDevelopmentIdentityRequestIsEnvironmentGatedAndNormalized(t *testing.T) {
	options := &DevelopmentIdentityOptions{
		Organizations: []DevelopmentIdentityOrganization{{ID: " lab ", Code: " main ", Name: " Pioneer Lab "}},
		Actors: []DevelopmentIdentityActor{{
			ID: " evaluator ", LoginID: " evaluator@example.test ", Name: " Evaluator ", RoleKey: " doctor ",
			OrganizationID: " lab ", InitialPassword: "EvaluationOnly1!",
		}},
	}
	if _, _, err := developmentIdentityRequest(config.Config{Environment: "production"}, options); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("production error=%v", err)
	}
	request, enabled, err := developmentIdentityRequest(config.Config{Environment: "demo"}, options)
	if err != nil || !enabled {
		t.Fatalf("enabled=%t error=%v", enabled, err)
	}
	if len(request.Organizations) != 1 || request.Organizations[0].ID != "lab" || request.Organizations[0].Code != "main" || request.Organizations[0].Name != "Pioneer Lab" {
		t.Fatalf("organizations=%+v", request.Organizations)
	}
	if len(request.Actors) != 1 || request.Actors[0].ID != "evaluator" || request.Actors[0].LoginID != "evaluator@example.test" || request.Actors[0].RoleKey != "doctor" || request.Actors[0].InitialPassword != "EvaluationOnly1!" {
		t.Fatalf("actors=%+v", request.Actors)
	}
}

func TestDevelopmentIdentityRequestRemainsDisabledWithoutFixtures(t *testing.T) {
	for _, options := range []*DevelopmentIdentityOptions{nil, {}} {
		request, enabled, err := developmentIdentityRequest(config.Config{Environment: "production"}, options)
		if err != nil || enabled || len(request.Actors) != 0 || len(request.Organizations) != 0 {
			t.Fatalf("options=%+v request=%+v enabled=%t error=%v", options, request, enabled, err)
		}
	}
}
