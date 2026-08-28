package capability

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	schedulervalidation "github.com/domainry/domainry-runtime/runtime/domain/scheduler/validation"
)

func TestSchedulerDefinitionAndScheduleExamplesExecuteOwnerValidator(t *testing.T) {
	for _, capability := range schedulerpolicy.SchedulerAuthoringDomain().Capabilities[:2] {
		for _, example := range capability.Examples {
			var err error
			if capability.Key == "scheduler.schedule" {
				err = schedulervalidation.SchedulerValidateScheduleFragment(t.Context(), example.Value)
			} else {
				err = schedulervalidation.SchedulerValidateDefinitionContract(t.Context(), example.Value)
			}
			if len(example.ExpectedErrorCodes) == 0 {
				if err != nil {
					t.Fatalf("capability=%s example=%s value=%#v err=%v", capability.Key, example.Name, example.Value, err)
				}
				continue
			}
			if code := apperror.CodeOf(err); code != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s value=%#v code=%s want=%s err=%v", capability.Key, example.Name, example.Value, code, example.ExpectedErrorCodes[0], err)
			}
		}
	}
}

func TestSchedulerAuthoringUsesOneCapabilityPerHTTPCommand(t *testing.T) {
	domain := schedulerpolicy.SchedulerAuthoringDomain()
	wantRoutes := map[string]string{
		"scheduler.job.simulate":        "POST /scheduler/jobs/{definitionID}/simulate",
		"scheduler.job.run":             "POST /operations/scheduler/definitions/{definitionID}/run",
		"scheduler.run.retry":           "POST /scheduler/runs/{runID}/retry",
		"scheduler.run.cancel":          "POST /scheduler/runs/{runID}/cancel",
		"scheduler.dead_letter.resolve": "POST /scheduler/dead-letters/{deadLetterID}/resolve",
	}
	for _, capability := range domain.Capabilities {
		route, ok := wantRoutes[capability.Key]
		if !ok {
			continue
		}
		if len(capability.ConfigurationRoutes) != 1 || capability.ConfigurationRoutes[0] != route {
			t.Errorf("capability=%s routes=%v want=%s", capability.Key, capability.ConfigurationRoutes, route)
		}
		delete(wantRoutes, capability.Key)
	}
	if len(wantRoutes) != 0 {
		t.Fatalf("missing scheduler command capabilities: %v", wantRoutes)
	}
}
