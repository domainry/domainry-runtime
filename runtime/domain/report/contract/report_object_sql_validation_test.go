package contract

import (
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestReportEngineObjectsPreservesJoinProofMetadata(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"payment": {
			Key: "payment",
			Fields: []definitionmodel.FieldSchema{{
				Key: "sale_id", Type: "relation", Unique: true,
				Validation: definitionmodel.FieldValidation{Target: "sale"},
				Config:     map[string]any{"cardinality": "one_to_one"},
			}},
		},
	}
	projected, err := ReportEngineObjects(objects)
	if err != nil {
		t.Fatal(err)
	}
	field := projected["payment"].Fields[0]
	if !field.Unique || field.RelationTarget != "sale" || field.RelationCardinality != "one_to_one" {
		t.Fatalf("field=%#v", field)
	}
}

func TestRuntimeReportCompilerDiscoversAndCanonicalizesSQLSources(t *testing.T) {
	schema := reportmodel.ReportObjectSQLSchema{SQL: `SELECT s.id AS sale_id, COUNT(DISTINCT p.id) AS payment_count
FROM sale s LEFT JOIN payment p ON p.sale_id = s.id GROUP BY s.id`}
	sources, err := ReportObjectSQLSourceObjects(schema)
	if err != nil || strings.Join(sources, ",") != "sale,payment" {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	objects := map[string]definitionmodel.ObjectSchema{
		"sale": {Key: "sale"},
		"payment": {Key: "payment", Fields: []definitionmodel.FieldSchema{{
			Key: "sale_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "sale"}, Config: map[string]any{"cardinality": "many_to_one"},
		}}},
	}
	canonical, plan, err := CanonicalReportObjectSQL(schema, objects)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(canonical.SourceObjects, ",") != "sale,payment" || len(canonical.ResultSchema) != 2 || len(canonical.JoinCardinalities) != 0 || plan.Sources[1].Cardinality != "one_to_many" {
		t.Fatalf("canonical=%#v plan=%#v", canonical, plan)
	}
}
