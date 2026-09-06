-- Historical SQLite schema fixture for the retired 020 tenant-initialization
-- authority. Runtime 021 reads these tables only through the adjudicating
-- Workspace migration and never creates them on a fresh database.
CREATE TABLE _audit_events (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, event TEXT NOT NULL,
  object_key TEXT, record_id TEXT, actor_id TEXT, role_key TEXT, summary TEXT,
  metadata_json TEXT NOT NULL, before_json TEXT NOT NULL, after_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
INSERT INTO _audit_events VALUES ('audit-020', 'workspace-primary', 'fixture.created', '', '', 'system', '', 'v020', '{}', '{}', '{}', '2026-08-30T00:00:00Z');

CREATE TABLE _workflow_executions (
  workspace_id TEXT NOT NULL, id TEXT NOT NULL, workflow_key TEXT NOT NULL,
  name TEXT NOT NULL, trigger TEXT NOT NULL, status TEXT NOT NULL, action_type TEXT,
  action_json TEXT NOT NULL, payload_json TEXT NOT NULL, result_json TEXT NOT NULL,
  process_id TEXT, node_id TEXT, object_key TEXT, record_id TEXT, actor_id TEXT,
  run_as TEXT, idempotency_key TEXT, attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 0, next_run_at TEXT, last_error TEXT,
  lease_owner TEXT NOT NULL DEFAULT '', lease_expires_at TEXT NOT NULL DEFAULT '',
  fencing_token BIGINT NOT NULL DEFAULT 0, message TEXT,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO _workflow_executions (
  workspace_id,id,workflow_key,name,trigger,status,action_json,payload_json,result_json,created_at,updated_at
) VALUES ('workspace-primary','workflow-020','fixture','Fixture','manual','completed','{}','{}','{}','2026-08-30T00:00:00Z','2026-08-30T00:00:00Z');

CREATE TABLE _workspaces (
  id TEXT PRIMARY KEY, canonical_code TEXT NOT NULL, name TEXT NOT NULL,
  status TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO _workspaces VALUES ('workspace-primary','primary','Primary','active','2026-08-30T00:00:00Z','2026-08-30T00:00:00Z');

CREATE TABLE _tenant_registry (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, canonical_code TEXT NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO _tenant_registry VALUES ('registry-primary','workspace-primary','primary','2026-08-30T00:00:00Z','2026-08-30T00:00:00Z');

CREATE TABLE _tenant_installation (
  installation_key TEXT PRIMARY KEY, tenant_registry_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL, initialized_at TEXT NOT NULL
);
INSERT INTO _tenant_installation VALUES ('primary','registry-primary','workspace-primary','2026-08-30T00:00:00Z');

CREATE TABLE _workspace_configuration (
  workspace_id TEXT PRIMARY KEY, configuration_json TEXT NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO _workspace_configuration VALUES (
  'workspace-primary',
  '{"plan":"standard","included_user_limit":1,"max_user_limit":100,"included_customer_limit":0,"max_customer_limit":10000,"included_store_limit":1,"max_stores":1,"contract_date":"2026-08-30","billing_day":1,"billing_contact_name":"","billing_contact_phone":"","billing_contact_email":"","billing_contact_address":"","billing_contact_notes":""}',
  '2026-08-30T00:00:00Z','2026-08-30T00:00:00Z'
);

CREATE TABLE _workspace_provisioning_receipts (
  request_id TEXT PRIMARY KEY, request_fingerprint TEXT NOT NULL,
  tenant_registry_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
  canonical_code TEXT NOT NULL, admin_login_id TEXT NOT NULL,
  must_change_password BOOLEAN NOT NULL, application_projection_ids_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
INSERT INTO _workspace_provisioning_receipts VALUES (
  'legacy-020','legacy-fingerprint','registry-primary','workspace-primary','primary',
  'owner@example.test',1,'{}','2026-08-30T00:00:00Z'
);
