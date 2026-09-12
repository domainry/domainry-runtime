package contract

import (
	"testing"

	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
)

// The capability payload and an Object's ux.config are the same binding written
// in two places; the only difference is the object key, which ux.config takes
// from the Object it hangs on.
func TestProfileBindingPayloadMatchesObjectUXConfig(t *testing.T) {
	payload := ProfileBindingAuthoringCapability().InputSchema
	if payload == nil {
		t.Fatal("capability publishes no input schema")
	}
	binding, declared := payload.Properties["payload"]
	if !declared {
		t.Fatal("input schema has no payload")
	}
	config := appschemacontract.IdentityProfileExtensionConfigSchema()
	for key := range config.Properties {
		if _, present := binding.Properties[key]; !present {
			t.Errorf("capability payload is missing %q, which ux.config accepts", key)
		}
	}
	for key := range binding.Properties {
		if key == "object_key" {
			continue
		}
		if _, present := config.Properties[key]; !present {
			t.Errorf("capability payload declares %q, which ux.config does not", key)
		}
	}
	if _, present := binding.Properties["object_key"]; !present {
		t.Error("capability payload must still take object_key")
	}
}
