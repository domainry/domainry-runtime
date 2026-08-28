package action

import (
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func TestActionExecutionPhaseMachineRejectsTerminalTransitions(t *testing.T) {
	machine := newActionExecutionPhaseMachine()
	if got := machine.current(); got != runtimeext.ExecutionPhasePrewrite {
		t.Fatalf("initial phase = %q", got)
	}
	if err := machine.beginWriting(); err != nil {
		t.Fatal(err)
	}
	if err := machine.beginWriting(); err != nil {
		t.Fatalf("repeated writing transition: %v", err)
	}
	if err := machine.commit(); err != nil {
		t.Fatal(err)
	}
	if got := machine.current(); got != runtimeext.ExecutionPhaseCommitted {
		t.Fatalf("committed phase = %q", got)
	}
	if err := machine.rollBack(); err == nil {
		t.Fatal("committed execution transitioned to rolled_back")
	}
	if got := machine.current(); got != runtimeext.ExecutionPhaseCommitted {
		t.Fatalf("phase changed after invalid transition = %q", got)
	}
}

func TestActionExecutionPhaseMachineAllowsPrewriteTerminalStates(t *testing.T) {
	committed := newActionExecutionPhaseMachine()
	if err := committed.commit(); err != nil || committed.current() != runtimeext.ExecutionPhaseCommitted {
		t.Fatalf("read-only commit phase=%q error=%v", committed.current(), err)
	}
	rolledBack := newActionExecutionPhaseMachine()
	if err := rolledBack.rollBack(); err != nil || rolledBack.current() != runtimeext.ExecutionPhaseRolledBack {
		t.Fatalf("prewrite rollback phase=%q error=%v", rolledBack.current(), err)
	}
}
