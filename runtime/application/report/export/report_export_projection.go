package export

import (
	"bytes"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

// EncodePreflightCSV uses the Data Exchange format engine to prove the hash of
// a bounded, exact result. Runtime still owns field selection and masking, but
// it does not carry a second CSV or artifact implementation.
func EncodePreflightCSV(rows []reportmodel.ReportResultRow, projection []string, maskedDimensions map[string]bool) ([]byte, error) {
	var output bytes.Buffer
	encoder := dataexchange.NewCSVEncoder(&output, 0)
	if err := encoder.Write(projection); err != nil {
		return nil, err
	}
	for _, source := range rows {
		row := make([]string, 0, len(projection))
		for _, key := range projection {
			value, ok := source.Dimensions[key]
			if !ok {
				value = source.Measures[key]
			}
			if maskedDimensions[key] && value != "" {
				value = "******"
			}
			row = append(row, value)
		}
		if err := encoder.Write(row); err != nil {
			return nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
