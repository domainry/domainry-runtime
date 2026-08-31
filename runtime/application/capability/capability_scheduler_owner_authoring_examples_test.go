package capability

import (
	"testing"

	"github.com/domainry/domainry-scheduler-sdk/schedule"
)

func TestSchedulerDefinitionAndScheduleExamplesExecuteOwnerValidator(t *testing.T) {
	for _, capability := range schedulerAuthoringDomain().Capabilities[:2] {
		for _, example := range capability.Examples {
			var err error
			if capability.Key == "scheduler.schedule" {
				err = schedule.ValidateData(t.Context(), example.Value)
			} else {
				err = schedule.ValidateDefinitionData(t.Context(), example.Value)
			}
			if len(example.ExpectedErrorCodes) == 0 {
				if err != nil {
					t.Fatalf("capability=%s example=%s value=%#v err=%v", capability.Key, example.Name, example.Value, err)
				}
				continue
			}
			if code := schedule.ValidationCode(err); code != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s value=%#v code=%s want=%s err=%v", capability.Key, example.Name, example.Value, code, example.ExpectedErrorCodes[0], err)
			}
		}
	}
}

func TestSchedulerAuthoringUsesOneCapabilityPerHTTPCommand(t *testing.T) {
	domain := schedulerAuthoringDomain()
	wantRoutes := map[string]string{
		"scheduler.job.simulate":        "POST /tenant-admin/scheduler/definitions/{definitionID}/simulate",
		"scheduler.job.run":             "POST /operations/scheduler/definitions/{definitionID}/run",
		"scheduler.run.retry":           "POST /operations/scheduler/runs/{runID}/retry",
		"scheduler.run.cancel":          "POST /operations/scheduler/runs/{runID}/cancel",
		"scheduler.dead_letter.resolve": "POST /operations/scheduler/dead-letters/{deadLetterID}/resolve",
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
