package action

import (
	"fmt"
	"sync"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

// actionExecutionPhaseMachine is Runtime-owned. Project Handlers may observe
// its current phase through runtimeext.ActionExecution, but cannot transition it.
type actionExecutionPhaseMachine struct {
	mu    sync.RWMutex
	phase runtimeext.ExecutionPhase
}

func newActionExecutionPhaseMachine() *actionExecutionPhaseMachine {
	return &actionExecutionPhaseMachine{phase: runtimeext.ExecutionPhasePrewrite}
}

func (m *actionExecutionPhaseMachine) current() runtimeext.ExecutionPhase {
	if m == nil {
		return runtimeext.ExecutionPhaseRolledBack
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.phase
}

func (m *actionExecutionPhaseMachine) allowsSynchronousConnectorCall() bool {
	return m.current() == runtimeext.ExecutionPhasePrewrite
}

func (m *actionExecutionPhaseMachine) beginWriting() error {
	return m.transition(runtimeext.ExecutionPhaseWriting)
}

func (m *actionExecutionPhaseMachine) commit() error {
	return m.transition(runtimeext.ExecutionPhaseCommitted)
}

func (m *actionExecutionPhaseMachine) rollBack() error {
	return m.transition(runtimeext.ExecutionPhaseRolledBack)
}

func (m *actionExecutionPhaseMachine) transition(next runtimeext.ExecutionPhase) error {
	if m == nil {
		return fmt.Errorf("action execution phase machine is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.phase == next {
		return nil
	}
	allowed := false
	switch m.phase {
	case runtimeext.ExecutionPhasePrewrite:
		allowed = next == runtimeext.ExecutionPhaseWriting || next == runtimeext.ExecutionPhaseCommitted || next == runtimeext.ExecutionPhaseRolledBack
	case runtimeext.ExecutionPhaseWriting:
		allowed = next == runtimeext.ExecutionPhaseCommitted || next == runtimeext.ExecutionPhaseRolledBack
	}
	if !allowed {
		return fmt.Errorf("invalid action execution phase transition: %s -> %s", m.phase, next)
	}
	m.phase = next
	return nil
}
