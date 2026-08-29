package metadata

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataReferenceEdgeProvider struct {
	graph changeplanmodel.ReferenceGraph
	err   error
}

func (p metadataReferenceEdgeProvider) Graph(context.Context, principalmodel.Principal) (changeplanmodel.ReferenceGraph, error) {
	return p.graph, p.err
}

func TestMetadataReferenceGraphBoundaries(t *testing.T) {
	admin := metadataSchemaAdmin()
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{})
	if _, err := service.ReferenceGraph(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	if _, err := service.ReferenceGraph(t.Context(), admin); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("missing provider error=%v", err)
	}
	edgeErr := errors.New("graph failed")
	service.references = metadataReferenceEdgeProvider{err: edgeErr}
	if _, err := service.ReferenceGraph(t.Context(), admin); !errors.Is(err, edgeErr) {
		t.Fatalf("provider error=%v", err)
	}
}
