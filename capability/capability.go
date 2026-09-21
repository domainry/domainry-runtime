// Package capability exposes Runtime's source-owned, deployment-neutral
// capability contracts without opening a Runtime or any infrastructure.
package capability

import "github.com/domainry/domainry-foundation/modulecapability"

type Inputs struct{}

// Open returns the deterministic set of Runtime-owned logical capability
// bindings. Runtime has several logical owners, so its public entry returns a
// collection while module repositories return one binding.
func Open(inputs Inputs) ([]modulecapability.Binding, error) {
	return openContract(inputs)
}
