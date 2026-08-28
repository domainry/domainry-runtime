package validation

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
)

func (state *validationState) validateBusinessIdentityBinding(path string, extension profilebindingmodel.Binding) {
	binding := extension.BusinessIdentity
	if strings.TrimSpace(binding.Key) == "" {
		state.add(path+".key", "is required")
	}
	if len(binding.SurfaceKeys) == 0 {
		state.add(path+".surface_keys", "must contain at least one surface key")
	}
	seenSurfaces := map[string]bool{}
	for index, surfaceKey := range binding.SurfaceKeys {
		surfaceKey = strings.TrimSpace(surfaceKey)
		if surfaceKey == "" {
			state.add(path+".surface_keys", "item %d must not be blank", index)
		} else if seenSurfaces[surfaceKey] {
			state.add(path+".surface_keys", "contains duplicate surface key %q", surfaceKey)
		}
		seenSurfaces[surfaceKey] = true
	}
	fields := state.fields[extension.ObjectKey]
	if binding.StatusField != "" {
		field := fields[binding.StatusField]
		if field.Key == "" {
			state.add(path+".status_field", "references unknown field %q", binding.StatusField)
		}
		if len(binding.ActiveStatusValues) == 0 {
			state.add(path+".active_status_values", "must contain at least one value when status_field is set")
		}
	} else if len(binding.ActiveStatusValues) > 0 {
		state.add(path+".active_status_values", "requires status_field")
	}
	if binding.BlacklistField != "" {
		field := fields[binding.BlacklistField]
		if field.Key == "" {
			state.add(path+".blacklist_field", "references unknown field %q", binding.BlacklistField)
		} else if field.Type != "boolean" {
			state.add(path+".blacklist_field", "field %q must have type boolean", binding.BlacklistField)
		}
	}
	seenClaims := map[string]bool{}
	for index, claim := range binding.Claims {
		claimPath := path + ".claims"
		claimKey := strings.TrimSpace(claim.ClaimKey)
		if claimKey == "" {
			state.add(claimPath, "item %d claim_key is required", index)
		} else if seenClaims[claimKey] {
			state.add(claimPath, "contains duplicate claim_key %q", claimKey)
		}
		seenClaims[claimKey] = true
		if field := fields[claim.FieldKey]; strings.TrimSpace(claim.FieldKey) == "" || field.Key == "" {
			state.add(claimPath, "item %d references unknown field %q", index, claim.FieldKey)
		}
	}
}
