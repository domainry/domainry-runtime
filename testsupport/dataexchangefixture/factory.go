package dataexchangefixture

import (
	"context"
	"fmt"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
)

// Factory supplies the SDK boundary required by Runtime composition tests.
// Source-owned implementation behavior is verified by external host composition,
// not by importing the implementation module into Runtime's dependency graph.
type Factory struct{}

func NewFactory() dataexchange.Factory { return Factory{} }

func (Factory) Open(context.Context, dataexchange.ApplicationRef) (dataexchange.Binding, error) {
	return Binding{}, nil
}

type Binding struct{}

func (Binding) Descriptor() dataexchange.Descriptor {
	return dataexchange.Descriptor{
		ProtocolVersion: dataexchange.ProtocolVersionV1,
		Mode:            dataexchange.DeploymentModeModule,
	}
}

func (Binding) SubmitImport(context.Context, dataexchange.ImportRequest) (dataexchange.Job, bool, error) {
	return dataexchange.Job{}, false, fmt.Errorf("Data Exchange fixture does not execute imports")
}

func (Binding) SubmitExport(context.Context, dataexchange.ExportRequest) (dataexchange.Job, bool, error) {
	return dataexchange.Job{}, false, fmt.Errorf("Data Exchange fixture does not execute exports")
}

func (Binding) Job(context.Context, dataexchange.JobRequest) (dataexchange.Job, error) {
	return dataexchange.Job{}, fmt.Errorf("Data Exchange fixture has no jobs")
}

func (Binding) Cancel(context.Context, dataexchange.JobRequest) (dataexchange.Job, error) {
	return dataexchange.Job{}, fmt.Errorf("Data Exchange fixture has no jobs")
}

func (Binding) Download(context.Context, dataexchange.JobRequest) (dataexchange.Artifact, error) {
	return dataexchange.Artifact{}, fmt.Errorf("Data Exchange fixture has no artifacts")
}

func (Binding) Start(context.Context, dataexchange.WorkerConfig) <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func (Binding) Close(context.Context) error { return nil }

var _ dataexchange.Factory = Factory{}
var _ dataexchange.Binding = Binding{}
