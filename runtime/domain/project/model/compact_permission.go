package projectmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// UnmarshalJSON keeps ordinary grants terse. An object is reserved for a
// permission with an actual data policy or denial-audit option.
func (permission *RolePermission) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		var declaration string
		if err := json.Unmarshal(raw, &declaration); err != nil {
			return err
		}
		parts := strings.Split(declaration, ";")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != parts[0] || parts[0] == "" {
			return fmt.Errorf("permission must be permission_key;data_scope")
		}
		scope := identitysdk.DataScope(parts[1])
		if !scope.Valid() {
			return fmt.Errorf("unsupported permission data scope %q", parts[1])
		}
		*permission = RolePermission{PermissionKey: parts[0], DataScope: scope}
		return nil
	}
	var expanded struct {
		PermissionKey string                         `json:"permission_key"`
		DataScope     identitysdk.DataScope          `json:"data_scope,omitempty"`
		DataPolicy    *identitysdk.ProjectDataPolicy `json:"data_policy,omitempty"`
		AuditDenial   bool                           `json:"audit_denial,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expanded); err != nil {
		return fmt.Errorf("permission must be a compact string or a policy object: %w", err)
	}
	if expanded.DataPolicy == nil && !expanded.AuditDenial {
		return fmt.Errorf("ordinary permission must use permission_key;data_scope")
	}
	*permission = RolePermission{
		PermissionKey: expanded.PermissionKey,
		DataScope:     expanded.DataScope,
		DataPolicy:    expanded.DataPolicy,
		AuditDenial:   expanded.AuditDenial,
	}
	return nil
}
