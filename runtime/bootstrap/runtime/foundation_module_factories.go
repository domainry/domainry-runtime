package runtime

import (
	"fmt"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
)

// FoundationModuleFactories are implementation choices owned by the outer
// product composition root. Runtime consumes only SDK contracts and host
// capabilities; it never imports or constructs these module implementations.
type FoundationModuleFactories struct {
	Audit     auditsdk.Factory
	Metadata  metadatasdk.Factory
	Lifecycle lifecyclesdk.Factory
}

func (f FoundationModuleFactories) Validate() error {
	if f.Audit == nil {
		return fmt.Errorf("project composition did not supply an Audit SDK Factory")
	}
	if f.Metadata == nil {
		return fmt.Errorf("project composition did not supply a Metadata SDK Factory")
	}
	if f.Lifecycle == nil {
		return fmt.Errorf("project composition did not supply a Lifecycle SDK Factory")
	}
	return nil
}
