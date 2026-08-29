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
	"golang.org/x/mod/sumdb/dirhash"
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

const identityModuleVersion = "v0.2.0-dev2"

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
	dependencies, dependencyVersions, err := publishDomainryDependencyClosure(repository, proxy)
	if err != nil {
		return publishResult{}, err
	}
	dependencySums, err := publishedDependencyGoSums(proxy, dependencies)
	if err != nil {
		return publishResult{}, err
	}
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
		if relative == "go.mod" {
			content, err = distributionGoModWithVersions(content, dependencyVersions)
			if err != nil {
				return publishResult{}, fmt.Errorf("prepare Runtime distribution go.mod: %w", err)
			}
		}
		if relative == "go.sum" && dependencySums != "" {
			content = mergeGoSum(content, dependencySums)
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
	// The immutable module version must describe the bytes we publish, not the
	// repository inputs. Distribution rewrites and sealed dependency checksums
	// are part of the module closure and can change while source files do not.
	version := contentVersion(source, files)
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
	goMod, err := os.ReadFile(filepath.Join(source, "go.mod"))
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

func publishedDependencyGoSums(proxy string, dependencies []publishedDependencyModule) (string, error) {
	lines := make([]string, 0, len(dependencies)*2)
	for _, dependency := range dependencies {
		escapedPath, err := module.EscapePath(dependency.Path)
		if err != nil {
			return "", err
		}
		escapedVersion, err := module.EscapeVersion(dependency.Version)
		if err != nil {
			return "", err
		}
		root := filepath.Join(proxy, filepath.FromSlash(escapedPath), "@v")
		zipSum, err := dirhash.HashZip(filepath.Join(root, escapedVersion+".zip"), dirhash.Hash1)
		if err != nil {
			return "", fmt.Errorf("hash published dependency %s@%s zip: %w", dependency.Path, dependency.Version, err)
		}
		modContent, err := os.ReadFile(filepath.Join(root, escapedVersion+".mod"))
		if err != nil {
			return "", err
		}
		modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(modContent))), nil
		})
		if err != nil {
			return "", fmt.Errorf("hash published dependency %s@%s go.mod: %w", dependency.Path, dependency.Version, err)
		}
		lines = append(lines,
			dependency.Path+" "+dependency.Version+" "+zipSum,
			dependency.Path+" "+dependency.Version+"/go.mod "+modSum,
		)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n", nil
}

func mergeGoSum(content []byte, additions string) []byte {
	lines := strings.FieldsFunc(string(content)+additions, func(r rune) bool { return r == '\n' || r == '\r' })
	unique := make(map[string]bool, len(lines))
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || unique[line] {
			continue
		}
		unique[line] = true
		result = append(result, line)
	}
	sort.Strings(result)
	return []byte(strings.Join(result, "\n") + "\n")
}

func publishDomainryDependencyClosure(repository, proxy string) ([]publishedDependencyModule, map[string]string, error) {
	goMod, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		return nil, nil, err
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("parse Runtime go.mod: %w", err)
	}
	selectedIdentityVersion := strings.TrimSpace(os.Getenv("DOMAINRY_IDENTITY_MODULE_VERSION"))
	if selectedIdentityVersion == "" {
		selectedIdentityVersion = identityModuleVersion
	}
	versions := map[string]string{"github.com/domainry/domainry-identity": selectedIdentityVersion}
	published := map[string]bool{}
	result := []publishedDependencyModule{}
	versionOverrides := map[string]string{}
	for _, local := range []struct {
		path, rootEnvironment, label string
		patterns                     []string
	}{
		{path: "github.com/domainry/domainry-foundation", rootEnvironment: "DOMAINRY_FOUNDATION_REPO_ROOT", label: "Foundation", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-audit-sdk", rootEnvironment: "DOMAINRY_AUDIT_SDK_REPO_ROOT", label: "Audit SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-audit", rootEnvironment: "DOMAINRY_AUDIT_REPO_ROOT", label: "Audit", patterns: []string{"./module"}},
		{path: "github.com/domainry/domainry-notification-sdk", rootEnvironment: "DOMAINRY_NOTIFICATION_SDK_REPO_ROOT", label: "Notification SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-notification", rootEnvironment: "DOMAINRY_NOTIFICATION_REPO_ROOT", label: "Notification", patterns: []string{"./module"}},
		{path: "github.com/domainry/domainry-party-sdk", rootEnvironment: "DOMAINRY_PARTY_SDK_REPO_ROOT", label: "Party SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-party", rootEnvironment: "DOMAINRY_PARTY_REPO_ROOT", label: "Party", patterns: []string{"./module"}},
		{path: "github.com/domainry/domainry-monitoring-sdk", rootEnvironment: "DOMAINRY_MONITORING_SDK_REPO_ROOT", label: "Monitoring SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-monitoring", rootEnvironment: "DOMAINRY_MONITORING_REPO_ROOT", label: "Monitoring", patterns: []string{"./module"}},
		{path: "github.com/domainry/domainry-scheduler-sdk", rootEnvironment: "DOMAINRY_SCHEDULER_SDK_REPO_ROOT", label: "Scheduler SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-scheduler", rootEnvironment: "DOMAINRY_SCHEDULER_REPO_ROOT", label: "Scheduler", patterns: []string{"./module", "./remote"}},
		{path: "github.com/domainry/domainry-data-exchange-sdk", rootEnvironment: "DOMAINRY_DATA_EXCHANGE_SDK_REPO_ROOT", label: "Data Exchange SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-data-exchange", rootEnvironment: "DOMAINRY_DATA_EXCHANGE_REPO_ROOT", label: "Data Exchange", patterns: []string{"./module", "./remote"}},
	} {
		root := strings.TrimSpace(os.Getenv(local.rootEnvironment))
		if root == "" {
			root = localReplacementRoot(repository, local.path)
		}
		if root == "" {
			continue
		}
		downloaded, packageErr := packageLocalModule(local.path, "v0.0.0", root, local.label, versionOverrides, true, local.patterns...)
		if packageErr != nil {
			return nil, nil, packageErr
		}
		identity, copyErr := copyDownloadedModule(proxy, downloaded)
		if copyErr != nil {
			return nil, nil, copyErr
		}
		result = append(result, identity)
		versionOverrides[local.path] = downloaded.Version
		published[local.path+"@"+downloaded.Version] = true
	}
	for _, requirement := range parsed.Require {
		if strings.HasPrefix(requirement.Mod.Path, "github.com/domainry/") {
			version := requirement.Mod.Version
			if overridden := versionOverrides[requirement.Mod.Path]; overridden != "" {
				version = overridden
			}
			versions[requirement.Mod.Path] = version
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
			downloaded, err := dependencyModule(repository, path, version)
			if err != nil {
				return nil, nil, err
			}
			moduleIdentity, err := copyDownloadedModule(proxy, downloaded)
			if err != nil {
				return nil, nil, err
			}
			result = append(result, moduleIdentity)
			dependencyMod, err := os.ReadFile(downloaded.GoMod)
			if err != nil {
				return nil, nil, err
			}
			dependency, err := modfile.Parse(downloaded.GoMod, dependencyMod, nil)
			if err != nil {
				return nil, nil, err
			}
			for _, requirement := range dependency.Require {
				if strings.HasPrefix(requirement.Mod.Path, "github.com/domainry/") {
					version := requirement.Mod.Version
					if overridden := versionOverrides[requirement.Mod.Path]; overridden != "" {
						version = overridden
					}
					if !published[requirement.Mod.Path+"@"+version] {
						next[requirement.Mod.Path] = version
					}
				}
			}
		}
		versions = next
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, versionOverrides, nil
}

func dependencyModule(repository, path, version string) (downloadedModule, error) {
	type localModule struct {
		path, rootEnvironment, label string
		patterns                     []string
	}
	for _, candidate := range []localModule{
		{path: "github.com/domainry/domainry-foundation", rootEnvironment: "DOMAINRY_FOUNDATION_REPO_ROOT", label: "Foundation", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-audit-sdk", rootEnvironment: "DOMAINRY_AUDIT_SDK_REPO_ROOT", label: "Audit SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-audit", rootEnvironment: "DOMAINRY_AUDIT_REPO_ROOT", label: "Audit", patterns: []string{"./module"}},
		{path: "github.com/domainry/domainry-identity", rootEnvironment: "DOMAINRY_IDENTITY_REPO_ROOT", label: "Identity", patterns: []string{"./module"}},
		{path: "github.com/domainry/domainry-notification-sdk", rootEnvironment: "DOMAINRY_NOTIFICATION_SDK_REPO_ROOT", label: "Notification SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-notification", rootEnvironment: "DOMAINRY_NOTIFICATION_REPO_ROOT", label: "Notification", patterns: []string{"./module"}},
		{path: "github.com/domainry/domainry-party-sdk", rootEnvironment: "DOMAINRY_PARTY_SDK_REPO_ROOT", label: "Party SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-party", rootEnvironment: "DOMAINRY_PARTY_REPO_ROOT", label: "Party", patterns: []string{"./module"}},
		{path: "github.com/domainry/domainry-scheduler-sdk", rootEnvironment: "DOMAINRY_SCHEDULER_SDK_REPO_ROOT", label: "Scheduler SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-scheduler", rootEnvironment: "DOMAINRY_SCHEDULER_REPO_ROOT", label: "Scheduler", patterns: []string{"./module", "./remote"}},
		{path: "github.com/domainry/domainry-data-exchange-sdk", rootEnvironment: "DOMAINRY_DATA_EXCHANGE_SDK_REPO_ROOT", label: "Data Exchange SDK", patterns: []string{"./..."}},
		{path: "github.com/domainry/domainry-data-exchange", rootEnvironment: "DOMAINRY_DATA_EXCHANGE_REPO_ROOT", label: "Data Exchange", patterns: []string{"./module", "./remote"}},
	} {
		root := strings.TrimSpace(os.Getenv(candidate.rootEnvironment))
		if root == "" {
			root = localReplacementRoot(repository, path)
		}
		if path == candidate.path && root != "" {
			return packageLocalModule(path, version, root, candidate.label, nil, false, candidate.patterns...)
		}
	}
	return downloadModule(repository, path, version)
}

func localReplacementRoot(repository, path string) string {
	content, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		return ""
	}
	parsed, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		return ""
	}
	for _, replacement := range parsed.Replace {
		if replacement.Old.Path != path || replacement.New.Version != "" || strings.TrimSpace(replacement.New.Path) == "" {
			continue
		}
		root := replacement.New.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(repository, root)
		}
		return filepath.Clean(root)
	}
	return ""
}

func packageLocalModule(path, version, rootValue, label string, versionOverrides map[string]string, contentAddressed bool, patterns ...string) (downloadedModule, error) {
	root, err := requiredDirectory(rootValue)
	if err != nil {
		return downloadedModule{}, fmt.Errorf("local %s module: %w", label, err)
	}
	if module.CheckPath(path) != nil || version == "" {
		return downloadedModule{}, fmt.Errorf("local %s module identity is invalid", label)
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return downloadedModule{}, err
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path != path {
		return downloadedModule{}, fmt.Errorf("local %s module path must be %s", label, path)
	}
	files, err := moduleBuildClosure(root, patterns...)
	if err != nil {
		return downloadedModule{}, fmt.Errorf("local %s module closure: %w", label, err)
	}
	source, err := os.MkdirTemp("", "domainry-local-module-source-*")
	if err != nil {
		return downloadedModule{}, err
	}
	defer os.RemoveAll(source)
	for _, relative := range files {
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if readErr != nil {
			return downloadedModule{}, readErr
		}
		if relative == "go.mod" {
			content, readErr = distributionGoModWithVersions(content, versionOverrides)
			if readErr != nil {
				return downloadedModule{}, fmt.Errorf("prepare local %s distribution go.mod: %w", label, readErr)
			}
			goMod = content
		}
		target := filepath.Join(source, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return downloadedModule{}, err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return downloadedModule{}, err
		}
	}
	if contentAddressed {
		contentIdentity := strings.TrimPrefix(contentVersion(source, files), "v0.0.0-source-")
		version = "v0.0.0-domainry." + contentIdentity[:16]
	}
	temporary, err := os.MkdirTemp("", "domainry-local-module-archive-*")
	if err != nil {
		return downloadedModule{}, err
	}
	infoPath := filepath.Join(temporary, "module.info")
	modPath := filepath.Join(temporary, "module.mod")
	zipPath := filepath.Join(temporary, "module.zip")
	info := []byte(fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-08-28T00:00:00Z\"}\n", version))
	if err := os.WriteFile(infoPath, info, 0o644); err != nil {
		return downloadedModule{}, err
	}
	if err := os.WriteFile(modPath, goMod, 0o644); err != nil {
		return downloadedModule{}, err
	}
	archive, err := os.Create(zipPath)
	if err != nil {
		return downloadedModule{}, err
	}
	if err := modzip.CreateFromDir(archive, module.Version{Path: path, Version: version}, source); err != nil {
		_ = archive.Close()
		return downloadedModule{}, fmt.Errorf("package local %s module: %w", label, err)
	}
	if err := archive.Close(); err != nil {
		return downloadedModule{}, err
	}
	return downloadedModule{Path: path, Version: version, Info: infoPath, GoMod: modPath, Zip: zipPath}, nil
}

// Local development replaces describe the checkout graph, not the immutable
// module graph published into the file proxy. Keeping ../ replaces in a
// published .mod or zip makes external consumers depend on the publisher's
// filesystem layout.
func distributionGoMod(content []byte) ([]byte, error) {
	return distributionGoModWithVersions(content, nil)
}

func distributionGoModWithVersions(content []byte, versions map[string]string) ([]byte, error) {
	parsed, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		return nil, err
	}
	for _, replacement := range append([]*modfile.Replace(nil), parsed.Replace...) {
		if err := parsed.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
			return nil, err
		}
	}
	// The Runtime production closure depends only on the deployment-neutral
	// Notification SDK. The implementation module is published beside Runtime
	// for Module compositions, but must be selected by the generated project;
	// keeping this test-only source requirement would force SaaS consumers to
	// download and trust an implementation they never link.
	if parsed.Module != nil && parsed.Module.Mod.Path == runtimeModulePath {
		if err := parsed.DropRequire("github.com/domainry/domainry-notification"); err != nil {
			return nil, err
		}
		if err := parsed.DropRequire("github.com/domainry/domainry-party"); err != nil {
			return nil, err
		}
		if err := parsed.DropRequire("github.com/domainry/domainry-monitoring"); err != nil {
			return nil, err
		}
	}
	for path, version := range versions {
		if strings.TrimSpace(version) == "" {
			continue
		}
		if parsed.Module != nil && parsed.Module.Mod.Path == runtimeModulePath && path == "github.com/domainry/domainry-notification" {
			continue
		}
		if parsed.Module != nil && parsed.Module.Mod.Path == runtimeModulePath && path == "github.com/domainry/domainry-party" {
			continue
		}
		if parsed.Module != nil && parsed.Module.Mod.Path == runtimeModulePath && path == "github.com/domainry/domainry-monitoring" {
			continue
		}
		if err := parsed.AddRequire(path, version); err != nil {
			return nil, err
		}
	}
	return parsed.Format()
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
	return moduleBuildClosure(repository, "./pkg/runtimehost", "./pkg/runtimeext")
}

func moduleBuildClosure(repository string, patterns ...string) ([]string, error) {
	arguments := append([]string{"list", "-deps", "-json"}, patterns...)
	command := exec.Command("go", arguments...)
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
			if path == "go.sum" && errors.Is(err, os.ErrNotExist) {
				continue
			}
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
