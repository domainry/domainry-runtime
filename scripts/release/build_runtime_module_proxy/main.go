package main

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

const runtimeModulePath = "github.com/domainry/domainry-runtime"

type listedPackage struct {
	Dir        string
	Standard   bool
	Module     *listedModule
	GoFiles    []string
	CgoFiles   []string
	CFiles     []string
	CXXFiles   []string
	MFiles     []string
	HFiles     []string
	FFiles     []string
	SFiles     []string
	SwigFiles  []string
	SwigCXX    []string
	SysoFiles  []string
	EmbedFiles []string
}

type listedModule struct {
	Main bool
}

type publishResult struct {
	ContractVersion       string                      `json:"contract_version"`
	ModulePath            string                      `json:"module_path"`
	Version               string                      `json:"version"`
	ZipSHA256             string                      `json:"zip_sha256"`
	GoModSHA256           string                      `json:"go_mod_sha256"`
	InfoSHA256            string                      `json:"info_sha256"`
	ListSHA256            string                      `json:"list_sha256"`
	FileCount             int                         `json:"file_count"`
	ClosureManifestSHA256 string                      `json:"closure_manifest_sha256"`
	DependencyModules     []publishedDependencyModule `json:"dependency_modules"`
}

type publishedDependencyModule struct {
	Path        string `json:"path"`
	Version     string `json:"version"`
	ZipSHA256   string `json:"zip_sha256"`
	GoModSHA256 string `json:"go_mod_sha256"`
	InfoSHA256  string `json:"info_sha256"`
	ListSHA256  string `json:"list_sha256"`
}

type downloadedModule struct {
	Path, Version, Info, GoMod, Zip, Error string
}

const identityModuleVersion = "v0.2.0-dev1"

func main() {
	repository := flag.String("repo", "", "Runtime repository root")
	proxy := flag.String("proxy", "", "Go module proxy output root")
	flag.Parse()
	result, err := publish(*repository, *proxy)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func publish(repositoryValue, proxyValue string) (publishResult, error) {
	repository, err := requiredDirectory(repositoryValue)
	if err != nil {
		return publishResult{}, fmt.Errorf("repository: %w", err)
	}
	proxy, err := requiredOutputDirectory(proxyValue)
	if err != nil {
		return publishResult{}, fmt.Errorf("proxy: %w", err)
	}
	if containsPath(repository, proxy) {
		return publishResult{}, errors.New("proxy output must be outside the Runtime repository")
	}
	files, err := runtimeBuildClosure(repository)
	if err != nil {
		return publishResult{}, err
	}
	version := contentVersion(repository, files)
	source, err := os.MkdirTemp("", "domainry-runtime-module-closure-*")
	if err != nil {
		return publishResult{}, err
	}
	defer os.RemoveAll(source)
	closureHash := sha256.New()
	for _, relative := range files {
		content, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(relative)))
		if err != nil {
			return publishResult{}, err
		}
		target := filepath.Join(source, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return publishResult{}, err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return publishResult{}, err
		}
		_, _ = closureHash.Write([]byte(relative))
		_, _ = closureHash.Write([]byte{0})
		_, _ = closureHash.Write([]byte(sha256Hex(content)))
		_, _ = closureHash.Write([]byte{0})
	}
	versionRoot := filepath.Join(proxy, filepath.FromSlash(runtimeModulePath), "@v")
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		return publishResult{}, err
	}
	zipPath := filepath.Join(versionRoot, version+".zip")
	archive, err := os.Create(zipPath)
	if err != nil {
		return publishResult{}, err
	}
	if err := modzip.CreateFromDir(archive, module.Version{Path: runtimeModulePath, Version: version}, source); err != nil {
		_ = archive.Close()
		return publishResult{}, fmt.Errorf("create Runtime module zip: %w", err)
	}
	if err := archive.Close(); err != nil {
		return publishResult{}, err
	}
	goMod, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		return publishResult{}, err
	}
	if err := os.WriteFile(filepath.Join(versionRoot, version+".mod"), goMod, 0o644); err != nil {
		return publishResult{}, err
	}
	info, err := json.Marshal(map[string]any{"Version": version, "Time": time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		return publishResult{}, err
	}
	info = append(info, '\n')
	if err := os.WriteFile(filepath.Join(versionRoot, version+".info"), info, 0o644); err != nil {
		return publishResult{}, err
	}
	list := []byte(version + "\n")
	if err := os.WriteFile(filepath.Join(versionRoot, "list"), list, 0o644); err != nil {
		return publishResult{}, err
	}
	dependencies, err := publishDomainryDependencyClosure(repository, proxy)
	if err != nil {
		return publishResult{}, err
	}
	zipContent, err := os.ReadFile(zipPath)
	if err != nil {
		return publishResult{}, err
	}
	return publishResult{
		ContractVersion:       "domainry-runtime-module-build-closure-v1",
		ModulePath:            runtimeModulePath,
		Version:               version,
		ZipSHA256:             sha256Hex(zipContent),
		GoModSHA256:           sha256Hex(goMod),
		InfoSHA256:            sha256Hex(info),
		ListSHA256:            sha256Hex(list),
		FileCount:             len(files),
		ClosureManifestSHA256: hex.EncodeToString(closureHash.Sum(nil)),
		DependencyModules:     dependencies,
	}, nil
}

func publishDomainryDependencyClosure(repository, proxy string) ([]publishedDependencyModule, error) {
	goMod, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		return nil, err
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return nil, fmt.Errorf("parse Runtime go.mod: %w", err)
	}
	versions := map[string]string{"github.com/domainry/domainry-identity": identityModuleVersion}
	published := map[string]bool{}
	result := []publishedDependencyModule{}
	for _, requirement := range parsed.Require {
		if strings.HasPrefix(requirement.Mod.Path, "github.com/domainry/") {
			versions[requirement.Mod.Path] = requirement.Mod.Version
		}
	}
	for len(versions) > 0 {
		paths := make([]string, 0, len(versions))
		for path := range versions {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		next := map[string]string{}
		for _, path := range paths {
			version := versions[path]
			identity := path + "@" + version
			if published[identity] {
				continue
			}
			published[identity] = true
			downloaded, err := downloadModule(repository, path, version)
			if err != nil {
				return nil, err
			}
			moduleIdentity, err := copyDownloadedModule(proxy, downloaded)
			if err != nil {
				return nil, err
			}
			result = append(result, moduleIdentity)
			dependencyMod, err := os.ReadFile(downloaded.GoMod)
			if err != nil {
				return nil, err
			}
			dependency, err := modfile.Parse(downloaded.GoMod, dependencyMod, nil)
			if err != nil {
				return nil, err
			}
			for _, requirement := range dependency.Require {
				if strings.HasPrefix(requirement.Mod.Path, "github.com/domainry/") {
					if !published[requirement.Mod.Path+"@"+requirement.Mod.Version] {
						next[requirement.Mod.Path] = requirement.Mod.Version
					}
				}
			}
		}
		versions = next
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func downloadModule(repository, path, version string) (downloadedModule, error) {
	command := exec.Command("go", "mod", "download", "-json", path+"@"+version)
	command.Dir = repository
	output, err := command.Output()
	if err != nil {
		return downloadedModule{}, fmt.Errorf("download private module %s@%s: %w", path, version, err)
	}
	var result downloadedModule
	if err := json.Unmarshal(output, &result); err != nil {
		return downloadedModule{}, err
	}
	if result.Error != "" || result.Path != path || result.Version != version || result.Info == "" || result.GoMod == "" || result.Zip == "" {
		return downloadedModule{}, fmt.Errorf("private module download is incomplete for %s@%s: %s", path, version, result.Error)
	}
	return result, nil
}

func copyDownloadedModule(proxy string, downloaded downloadedModule) (publishedDependencyModule, error) {
	escapedPath, err := module.EscapePath(downloaded.Path)
	if err != nil {
		return publishedDependencyModule{}, err
	}
	escapedVersion, err := module.EscapeVersion(downloaded.Version)
	if err != nil {
		return publishedDependencyModule{}, err
	}
	root := filepath.Join(proxy, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return publishedDependencyModule{}, err
	}
	identity := publishedDependencyModule{Path: downloaded.Path, Version: downloaded.Version}
	for extension, source := range map[string]string{".info": downloaded.Info, ".mod": downloaded.GoMod, ".zip": downloaded.Zip} {
		content, err := os.ReadFile(source)
		if err != nil {
			return publishedDependencyModule{}, err
		}
		if err := os.WriteFile(filepath.Join(root, escapedVersion+extension), content, 0o644); err != nil {
			return publishedDependencyModule{}, err
		}
		switch extension {
		case ".info":
			identity.InfoSHA256 = sha256Hex(content)
		case ".mod":
			identity.GoModSHA256 = sha256Hex(content)
		case ".zip":
			identity.ZipSHA256 = sha256Hex(content)
		}
	}
	list := []byte(downloaded.Version + "\n")
	if err := os.WriteFile(filepath.Join(root, "list"), list, 0o644); err != nil {
		return publishedDependencyModule{}, err
	}
	identity.ListSHA256 = sha256Hex(list)
	return identity, nil
}

func runtimeBuildClosure(repository string) ([]string, error) {
	command := exec.Command("go", "list", "-deps", "-json", "./pkg/runtimehost", "./pkg/runtimeext")
	command.Dir = repository
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bufio.NewReader(output))
	files := map[string]bool{"go.mod": true, "go.sum": true}
	for {
		var listed listedPackage
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			_ = command.Wait()
			return nil, fmt.Errorf("decode go list package: %w", err)
		}
		if listed.Standard || listed.Module == nil || !listed.Module.Main {
			continue
		}
		names := [][]string{
			listed.GoFiles, listed.CgoFiles, listed.CFiles, listed.CXXFiles,
			listed.MFiles, listed.HFiles, listed.FFiles, listed.SFiles,
			listed.SwigFiles, listed.SwigCXX, listed.SysoFiles, listed.EmbedFiles,
		}
		for _, collection := range names {
			for _, name := range collection {
				absolute := filepath.Join(listed.Dir, name)
				relative, err := filepath.Rel(repository, absolute)
				if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					return nil, fmt.Errorf("build closure escaped repository: %s", absolute)
				}
				clean := filepath.ToSlash(relative)
				if forbiddenRuntimeModulePath(clean) {
					return nil, fmt.Errorf("forbidden file entered Runtime build closure: %s", clean)
				}
				files[clean] = true
			}
		}
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("go list Runtime build closure: %w", err)
	}
	result := make([]string, 0, len(files))
	for path := range files {
		if _, err := os.Stat(filepath.Join(repository, filepath.FromSlash(path))); err != nil {
			return nil, fmt.Errorf("build closure file %s: %w", path, err)
		}
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func forbiddenRuntimeModulePath(path string) bool {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, "_test.go") ||
		strings.Contains(lower, "golden") ||
		strings.Contains(lower, "fixture") {
		return true
	}
	for _, prefix := range []string{"docs/", "scripts/", "evals/", "frontend/", "examples/", "skills/", ".artifacts/"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func contentVersion(repository string, files []string) string {
	hash := sha256.New()
	for _, relative := range files {
		content, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(relative)))
		if err != nil {
			panic(err)
		}
		_, _ = hash.Write([]byte(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(content)
		_, _ = hash.Write([]byte{0})
	}
	return "v0.0.0-source-" + hex.EncodeToString(hash.Sum(nil))[:16]
}

func requiredDirectory(value string) (string, error) {
	path, err := filepath.Abs(filepath.Clean(strings.TrimSpace(value)))
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("must be one real directory")
	}
	return path, nil
}

func requiredOutputDirectory(value string) (string, error) {
	path, err := filepath.Abs(filepath.Clean(strings.TrimSpace(value)))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	return requiredDirectory(path)
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func listZipPaths(path string) ([]string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	result := make([]string, 0, len(archive.File))
	for _, entry := range archive.File {
		result = append(result, entry.Name)
	}
	sort.Strings(result)
	return result, nil
}
