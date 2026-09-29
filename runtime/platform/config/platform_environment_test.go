package config

import "testing"

func TestDeckProductProductionEnvironmentLoadsAsRuntimeContract(t *testing.T) {
	values := map[string]string{
		"APP_ENV":                                   "production",
		"DATABASE_URL":                              "mysql://product:local-test-password@tidb.example.com:4000/domainry_prod_a05fa06bfa25780f2503acaa34b25a93",
		"DATABASE_DRIVER":                           "mysql",
		"DATABASE_MIGRATION_MODE":                   "apply",
		"HTTP_PUBLIC_ADDR":                          "0.0.0.0:8080",
		"HTTP_MANAGEMENT_ADDR":                      "127.0.0.1:8082",
		"HTTP_OPS_ADDR":                             "127.0.0.1:8083",
		"HTTP_PUBLIC_ORIGINS":                       "https://product.example.com",
		"HTTP_MANAGEMENT_ORIGINS":                   "https://product-management.example.com",
		"HTTP_OPS_ORIGINS":                          "https://product-ops.example.com",
		"CORS_ALLOWED_ORIGINS":                      "https://product.example.com,https://product-management.example.com,https://product-ops.example.com",
		"IDENTITY_REDIRECT_URLS":                    "https://product.example.com/auth/callback",
		"IDENTITY_BROWSER_RETURN_URLS":              "https://product.example.com/auth/callback",
		"AUTH_JWT_SECRET":                           "local-test-jwt-secret",
		"AUTH_JWT_ACTIVE_KID":                       "product-v1",
		"AUTH_DEFAULT_PASSWORD":                     "local-test-default-password",
		"IDENTITY_DATA_SECRET_KEY":                  "local-test-data-secret",
		"IDENTITY_DATA_ACTIVE_KEY_ID":               "product-v1",
		"IDENTITY_OPERATIONS_ACCESS_TOKEN":          "local-test-operations-token",
		"AUDIT_EXPORT_TOKEN_KEY":                    "local-test-audit-key",
		"INTEGRATION_SECRET_KEY":                    "local-test-integration-key",
		"INTEGRATION_ACTIVE_KEY_ID":                 "product-v1",
		"INTEGRATION_GOOGLE_QUOTA_PROJECT_ID":       "project-a",
		"INTEGRATION_GOOGLE_QUOTA_SERVICES":         "gmail.googleapis.com",
		"INTEGRATION_GOOGLE_QUOTA_CREDENTIALS_FILE": "/run/secrets/google-quota.json",
		"INTEGRATION_GOOGLE_QUOTA_TIMEOUT":          "15s",
		"UPLOAD_DIR":                                "/app/uploads",
		"INITIAL_WORKSPACE_CREDENTIAL_FILE":         "/app/data/initial-workspace-credential.json",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	configured, _, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configured.DatabaseDriver != "mysql" || configured.DatabaseDSN == "" || len(configured.IdentityRedirectURLs) != 1 || configured.IdentityRedirectURLs[0] != values["IDENTITY_REDIRECT_URLS"] {
		t.Fatal("the product environment did not produce the expected Runtime database and redirect configuration")
	}
}
