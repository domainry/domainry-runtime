package record

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
)

type recordDataExchangeBindingProbe struct {
	mu              sync.Mutex
	jobs            map[string]dataexchange.Job
	artifact        dataexchange.Artifact
	artifactContent string
	importErr       error
	exportErr       error
	lastImport      dataexchange.ImportRequest
	lastExport      dataexchange.ExportRequest
	started         int
}

func (*recordDataExchangeBindingProbe) Descriptor() dataexchange.Descriptor {
	return dataexchange.Descriptor{ProtocolVersion: dataexchange.ProtocolVersionV1, Mode: dataexchange.DeploymentModeModule}
}

func (p *recordDataExchangeBindingProbe) SubmitImport(_ context.Context, request dataexchange.ImportRequest) (dataexchange.Job, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastImport = request
	if p.importErr != nil {
		return dataexchange.Job{}, false, p.importErr
	}
	return p.submit(request.Scope, "records", "import", request.ObjectKey, request.IdempotencyKey, nil), false, nil
}

func (p *recordDataExchangeBindingProbe) SubmitExport(_ context.Context, request dataexchange.ExportRequest) (dataexchange.Job, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastExport = request
	if p.exportErr != nil {
		return dataexchange.Job{}, false, p.exportErr
	}
	return p.submit(request.Scope, request.Provider, "export", request.ObjectKey, request.IdempotencyKey, request.Options), false, nil
}

func (p *recordDataExchangeBindingProbe) submit(scope dataexchange.Scope, provider, operation, objectKey, key string, options []byte) dataexchange.Job {
	if p.jobs == nil {
		p.jobs = map[string]dataexchange.Job{}
	}
	id := "data_exchange:" + strings.TrimSpace(key)
	now := time.Now().UTC()
	job := dataexchange.Job{ID: id, Provider: provider, Operation: operation, Status: "queued", WorkspaceID: scope.WorkspaceID, ObjectKey: objectKey, ActorID: scope.ActorID, RoleKey: scope.RoleKey, Options: append([]byte(nil), options...), CreatedAt: now, UpdatedAt: now}
	p.jobs[id] = job
	return job
}

func (p *recordDataExchangeBindingProbe) Job(_ context.Context, request dataexchange.JobRequest) (dataexchange.Job, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jobs[request.JobID], nil
}

func (p *recordDataExchangeBindingProbe) Cancel(ctx context.Context, request dataexchange.JobRequest) (dataexchange.Job, error) {
	p.mu.Lock()
	job := p.jobs[request.JobID]
	job.Status = "cancelled"
	p.jobs[request.JobID] = job
	p.mu.Unlock()
	return p.Job(ctx, request)
}

func (p *recordDataExchangeBindingProbe) Download(_ context.Context, _ dataexchange.JobRequest) (dataexchange.Artifact, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	artifact := p.artifact
	artifact.Content = io.NopCloser(strings.NewReader(p.artifactContent))
	return artifact, nil
}

func (p *recordDataExchangeBindingProbe) Start(context.Context, dataexchange.WorkerConfig) <-chan struct{} {
	p.mu.Lock()
	p.started++
	p.mu.Unlock()
	return workerplatform.Stopped()
}

func (*recordDataExchangeBindingProbe) Close(context.Context) error { return nil }

var _ dataexchange.Binding = (*recordDataExchangeBindingProbe)(nil)
