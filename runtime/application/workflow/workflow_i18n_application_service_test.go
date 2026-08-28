package workflow

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWorkflowProductionCodeUsesLocalizationKeys(t *testing.T) {
	han := regexp.MustCompile(`\p{Han}`)
	files, err := filepath.Glob("workflow*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if location := han.FindIndex(content); location != nil {
			t.Errorf("%s contains a hard-coded Chinese production message near byte %d; return a stable localization key instead", file, location[0])
		}
	}
}
