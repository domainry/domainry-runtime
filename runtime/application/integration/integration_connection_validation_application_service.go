package integration

import (
	"errors"
	"sort"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func ValidateConnectionConfig(connector integrationmodel.ConnectorSchema, providerKey, status string, config map[string]any) error {
	return integrationConnectionValidationApplicationError(integrationcontract.IntegrationValidateConnectionConfig(connector, providerKey, status, config))
}

func ValidateProviderConfig(connector integrationmodel.ConnectorSchema, providerKey string, config map[string]any) error {
	return integrationConnectionValidationApplicationError(integrationcontract.IntegrationValidateProviderConfig(connector, providerKey, config))
}

func integrationConnectionValidationApplicationError(err error) error {
	if err == nil {
		return nil
	}
	var coded interface {
		ErrorCode() string
		ErrorParams() map[string]string
	}
	if !errors.As(err, &coded) {
		return err
	}
	params := coded.ErrorParams()
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		values = append(values, key, params[key])
	}
	return badRequest(coded.ErrorCode(), values...)
}
