package productbrand

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

const NameEnvironmentVariable = "PRODUCT_BRAND_NAME"

// ResolveName keeps blank deployment overrides from erasing the repository
// product brand default.
func ResolveName(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return DefaultName
}

func NameFromEnvironment() string {
	return ResolveName(os.Getenv(NameEnvironmentVariable))
}

func NameRevision(value string) string {
	digest := sha256.Sum256([]byte(ResolveName(value)))
	return hex.EncodeToString(digest[:8])
}
