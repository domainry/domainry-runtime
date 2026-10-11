package runtimehost

import (
	"strings"
	"testing"
	"unicode"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestInitialWorkspaceAdministratorCredentialUsesFixedDevelopmentPassword(t *testing.T) {
	password, mustChangePassword, err := initialWorkspaceAdministratorCredential(config.Config{
		Environment:                           "development",
		AuthDefaultPassword:                   "fixed-development-password",
		AuthInitialCredentialRotationRequired: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if password != "fixed-development-password" || mustChangePassword {
		t.Fatalf("development credential = (%q, %t)", password, mustChangePassword)
	}
}

func TestInitialWorkspaceAdministratorCredentialGeneratesProductionPassword(t *testing.T) {
	first, firstMustChange, err := initialWorkspaceAdministratorCredential(config.Config{
		Environment:                           "production",
		AuthDefaultPassword:                   "must-not-be-used",
		AuthInitialCredentialRotationRequired: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, secondMustChange, err := initialWorkspaceAdministratorCredential(config.Config{Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first == "must-not-be-used" || !firstMustChange || !secondMustChange {
		t.Fatalf("production credentials were not independently generated with forced rotation")
	}
	for _, password := range []string{first, second} {
		if len(password) > 72 || !passwordSatisfiesInitialCredentialPolicy(password) {
			t.Fatalf("generated password does not satisfy the initial credential policy")
		}
	}
}

func passwordSatisfiesInitialCredentialPolicy(password string) bool {
	var upper, lower, number, symbol bool
	for _, character := range password {
		upper = upper || unicode.IsUpper(character)
		lower = lower || unicode.IsLower(character)
		number = number || unicode.IsDigit(character)
		symbol = symbol || unicode.IsPunct(character) || unicode.IsSymbol(character)
	}
	return len(strings.TrimSpace(password)) >= 32 && upper && lower && number && symbol
}
