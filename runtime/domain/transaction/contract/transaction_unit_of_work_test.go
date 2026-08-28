package contract_test

import (
	"context"
	"testing"

	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
)

type recordPorts interface {
	transactioncontract.PortSet
	Save(context.Context, string) error
}

type recordPortsProbe struct {
	saved string
}

func (p *recordPortsProbe) Save(_ context.Context, value string) error {
	p.saved = value
	return nil
}

type unitOfWorkProbe struct {
	ports recordPorts
}

func (u unitOfWorkProbe) WithinTransaction(ctx context.Context, operation transactioncontract.Operation[recordPorts]) error {
	return operation(ctx, u.ports)
}

func TestUnitOfWorkProvidesOnlyOwnerPortSet(t *testing.T) {
	ports := &recordPortsProbe{}
	var unit transactioncontract.UnitOfWork[recordPorts] = unitOfWorkProbe{ports: ports}
	if err := unit.WithinTransaction(t.Context(), func(ctx context.Context, current recordPorts) error {
		return current.Save(ctx, "record-1")
	}); err != nil {
		t.Fatal(err)
	}
	if ports.saved != "record-1" {
		t.Fatalf("saved = %q", ports.saved)
	}
}
