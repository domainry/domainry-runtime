package appschema

import (
	"fmt"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
)

func (s ApplicationSchemaStore) metadataDefinitions() (metadatasdk.Definitions, error) {
	if s.metadata == nil || s.metadata.Definitions() == nil {
		return nil, fmt.Errorf("Metadata definitions are unavailable")
	}
	return s.metadata.Definitions(), nil
}
