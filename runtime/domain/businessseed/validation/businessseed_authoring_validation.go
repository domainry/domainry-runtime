package validation

import (
	"regexp"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

var businessSeedKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

func ValidateBusinessSeedKey(seedKey string) error {
	seedKey = strings.TrimSpace(seedKey)
	if seedKey == "" {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.business_seed.key_required"}
	}
	if !businessSeedKeyPattern.MatchString(seedKey) {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.business_seed.key_invalid", Params: map[string]string{"seed_key": seedKey}}
	}
	return nil
}

func BusinessSeedRecordID(objectKey, seedKey string) string {
	return strings.TrimSpace(objectKey) + "_" + strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(seedKey))
}
