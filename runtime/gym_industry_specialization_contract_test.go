package runtime_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeProductionCodeHasNoGymIndustrySpecialization(t *testing.T) {
	forbidden := []string{
		"gym_member", "gym_student", "gym_coach", "gym_staff",
		"membership_card", "stored_value_account", "stored_value_transaction",
		"personal_training_package", "personal_training_session", "group_class_booking",
		"equipment_repair_ticket", "coach_commission_settlement",
	}
	root := "."
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == "testdata" {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(content))
		for _, term := range forbidden {
			if strings.Contains(lower, term) {
				t.Errorf("Runtime production file %s contains industry-specific term %q", path, term)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
