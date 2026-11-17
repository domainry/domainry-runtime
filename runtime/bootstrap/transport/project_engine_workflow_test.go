package transport

import (
	"context"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestProjectEngineRecordWorkflowRequiresCurrentIdentityAndOwner(t *testing.T) {
	e := &projectEngine{principal: func(context.Context) (principalmodel.Principal, bool) { return principalmodel.Principal{}, false }}
	if _, err := e.ReadRecordWorkflow(t.Context(), "case", "record", "flow"); runtimeengine.HTTPStatus(err) != 401 {
		t.Fatal(err)
	}
	e.principal = projectKnowledgePrincipal
	if _, err := e.ReadRecordWorkflow(t.Context(), "case", "record", "flow"); runtimeengine.HTTPStatus(err) != 503 {
		t.Fatal(err)
	}
}

var _ runtimeengine.RecordWorkflowReader = (*projectEngine)(nil)
