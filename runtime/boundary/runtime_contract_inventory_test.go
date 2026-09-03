package boundary_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var emptyWorkspaceComparisonPattern = regexp.MustCompile(`(?:==|!=)\s*""`)

func TestRuntimeGeneratedIdempotencyInventoryIsCurrent(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	command := exec.Command("go", "run", "scripts/contracts/runtime_idempotency_entrypoint_inventory.go", "--check")
	command.Dir = repositoryRoot
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Runtime idempotency inventory is not current: %v\n%s", err, output)
	}
}

// TestRuntimeWorkspaceFallbackReviewBaseline makes every production-code
// workspace fallback candidate a reviewed, line-content-bound decision. New or
// changed candidates fail this boundary until the exact baseline is reviewed;
// removing a fallback deliberately tightens the baseline.
func TestRuntimeWorkspaceFallbackReviewBaseline(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	baselinePath := filepath.Join(repositoryRoot, "docs", "architecture", "runtime-workspace-fallback-review-baseline.txt")
	expectedRaw, err := os.ReadFile(baselinePath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	expected := nonBlankSortedLines(string(expectedRaw))
	actual := runtimeWorkspaceFallbackFindings(t, repositoryRoot)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("Runtime workspace fallback review baseline changed; remove the fallback or explicitly review this exact list:\n%s", strings.Join(actual, "\n"))
	}
}

func runtimeWorkspaceFallbackFindings(t *testing.T, repositoryRoot string) []string {
	t.Helper()
	findings := []string{}
	for _, relativeRoot := range []string{"runtime/application", "runtime/infrastructure"} {
		root := filepath.Join(repositoryRoot, filepath.FromSlash(relativeRoot))
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			relative, _ := filepath.Rel(repositoryRoot, path)
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				lower := strings.ToLower(line)
				if !strings.Contains(lower, "workspace") || (!emptyWorkspaceComparisonPattern.MatchString(line) && !strings.Contains(lower, `"default"`)) {
					continue
				}
				digest := sha256.Sum256([]byte(line))
				findings = append(findings, filepath.ToSlash(relative)+"|"+hex.EncodeToString(digest[:8]))
			}
			return scanner.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(findings)
	return findings
}
