package integrations

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"testing"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

func TestLocalizeConnectorCatalogResolvesConnectorProviderAndOperationWithoutChangingContract(t *testing.T) {
	localized := func(english, chinese string) localizationmodel.LocalizedTextMap {
		return localizationmodel.LocalizedTextMap{"en-US": {"name": english, "description": english + " description"}, "zh-CN": {"name": chinese, "description": chinese + "说明"}}
	}
	original := []integrationmodel.ConnectorSchema{{
		Key: "payment", Name: "Payment", Description: "Payment description", I18n: localized("Payment", "支付"),
		Providers:  []integrationmodel.ConnectorProviderSchema{{Key: "stripe", Name: "Stripe", Description: "Stripe description", I18n: localized("Stripe", "Stripe 支付")}},
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "charge", Name: "Charge", Description: "Charge description", I18n: localized("Charge", "扣款")}},
	}}

	result := localizeConnectorCatalog(original, "zh-CN")
	if result[0].Name != "支付" || result[0].Description != "支付说明" || result[0].Providers[0].Name != "Stripe 支付" || result[0].Operations[0].Name != "扣款" {
		t.Fatalf("localized catalog=%+v", result)
	}
	if original[0].Name != "Payment" || original[0].Providers[0].Name != "Stripe" || original[0].Operations[0].Name != "Charge" {
		t.Fatalf("localization mutated contract source: %+v", original)
	}
}

func TestLocalizedConnectorPropertyFallsBack(t *testing.T) {
	if got := localizedConnectorProperty(nil, "fr-FR", "name", "Fallback"); got != "Fallback" {
		t.Fatalf("fallback=%q", got)
	}
}
