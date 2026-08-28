package openapi

func addOperationsControlOpenAPIPaths(paths map[string]any) {
	paths["/operations/controls"] = map[string]any{
		"get": openAPIOperation("listRuntimeOperationControls", "Operations", "List runtime-global operation control state", openAPIAdminSecurity(), openAPIJSONResponse("Operation controls", openAPIObject(nil))),
	}
	paths["/operations/controls/{controlKind}/{owner}"] = map[string]any{
		"put": openAPIOperation("setRuntimeOperationControl", "Operations", "Set an audited runtime-global operation control", openAPIAdminSecurity(), openAPIPathParameter("controlKind", "Control kind"), openAPIPathParameter("owner", "Control owner"), openAPIJSONResponse("Operation control", openAPIObject(nil))),
	}
	paths["/operations/database-retirements"] = map[string]any{
		"get":  openAPIOperation("listDatabaseRetirements", "Operations", "List database retirement evidence", openAPIAdminSecurity(), openAPIJSONResponse("Database retirements", openAPIObject(nil))),
		"post": openAPIOperation("discoverDatabaseRetirement", "Operations", "Create a database retirement discovery record", openAPIAdminSecurity(), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Database retirement", openAPIObject(nil))),
	}
	paths["/operations/database-retirements/{retirementID}"] = map[string]any{
		"get": openAPIOperation("getDatabaseRetirement", "Operations", "Get database retirement evidence", openAPIAdminSecurity(), openAPIPathParameter("retirementID", "Retirement ID"), openAPIJSONResponse("Database retirement", openAPIObject(nil))),
	}
	for _, action := range []string{"preview", "advance", "execute"} {
		paths["/operations/database-retirements/{retirementID}/"+action] = map[string]any{
			"post": openAPIOperation(action+"DatabaseRetirement", "Operations", action+" a database retirement state", openAPIAdminSecurity(), openAPIPathParameter("retirementID", "Retirement ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Database retirement", openAPIObject(nil))),
		}
	}
}
