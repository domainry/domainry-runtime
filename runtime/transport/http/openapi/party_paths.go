package openapi

func addPartyOpenAPIPaths(paths map[string]any) {
	paths["/party"] = map[string]any{
		"get": openAPIOperation("listParties", "Party", "List optional Foundation parties", openAPIAdminSecurity(), openAPIJSONResponse("Parties", openAPIObject(nil))),
	}
	paths["/party/{partyID}"] = map[string]any{
		"get": openAPIOperation("getParty", "Party", "Get one person or organization aggregate", openAPIAdminSecurity(), openAPIPathParameter("partyID", "Party ID"), openAPIJSONResponse("Party", openAPIObject(nil))),
		"put": openAPIOperation("upsertParty", "Party", "Create or replace one person or organization aggregate", openAPIAdminSecurity(), openAPIPathParameter("partyID", "Party ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Party", openAPIObject(nil))),
	}
	paths["/foundation/jobs"] = map[string]any{
		"get": openAPIOperation("listFoundationJobs", "Party", "List optional Foundation job catalog items", openAPIAdminSecurity(), openAPIJSONResponse("Jobs", openAPIObject(nil))),
	}
	paths["/foundation/jobs/{jobID}"] = map[string]any{
		"get": openAPIOperation("getFoundationJob", "Party", "Get one job catalog item", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Job ID"), openAPIJSONResponse("Job", openAPIObject(nil))),
		"put": openAPIOperation("upsertFoundationJob", "Party", "Create or replace one job catalog item", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Job ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Job", openAPIObject(nil))),
	}
	paths["/foundation/positions"] = map[string]any{
		"get": openAPIOperation("listFoundationPositions", "Party", "List optional Foundation positions", openAPIAdminSecurity(), openAPIJSONResponse("Positions", openAPIObject(nil))),
	}
	paths["/foundation/positions/{positionID}"] = map[string]any{
		"get": openAPIOperation("getFoundationPosition", "Party", "Get one position", openAPIAdminSecurity(), openAPIPathParameter("positionID", "Position ID"), openAPIJSONResponse("Position", openAPIObject(nil))),
		"put": openAPIOperation("upsertFoundationPosition", "Party", "Create or replace one position", openAPIAdminSecurity(), openAPIPathParameter("positionID", "Position ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Position", openAPIObject(nil))),
	}
	paths["/foundation/organization-extensions"] = map[string]any{
		"get": openAPIOperation("listFoundationOrganizationExtensions", "Party", "List Territory, Team, Store, and Warehouse extensions", openAPIAdminSecurity(), openAPIJSONResponse("Organization extensions", openAPIObject(nil))),
	}
	paths["/foundation/organization-extensions/{extensionID}"] = map[string]any{
		"put": openAPIOperation("upsertFoundationOrganizationExtension", "Party", "Create or replace one organization extension", openAPIAdminSecurity(), openAPIPathParameter("extensionID", "Extension ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Organization extension", openAPIObject(nil))),
	}
	paths["/foundation/organization-memberships"] = map[string]any{
		"get": openAPIOperation("listFoundationOrganizationMemberships", "Party", "List effective Workforce memberships for organization extensions", openAPIAdminSecurity(), openAPIJSONResponse("Organization memberships", openAPIObject(nil))),
	}
	paths["/foundation/organization-memberships/{membershipID}"] = map[string]any{
		"put": openAPIOperation("upsertFoundationOrganizationMembership", "Party", "Create or replace one organization extension membership", openAPIAdminSecurity(), openAPIPathParameter("membershipID", "Membership ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Organization membership", openAPIObject(nil))),
	}
}
