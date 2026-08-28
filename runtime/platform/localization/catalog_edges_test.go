package localization

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

type catalogReadFailureFS struct{ fs.FS }

func (files catalogReadFailureFS) Open(name string) (fs.File, error) {
	if strings.HasSuffix(name, ".toml") {
		return nil, errors.New("read failed")
	}
	return files.FS.Open(name)
}

func TestCatalogLookupResourcesFormatAndPanics(t *testing.T) {
	locales := SupportedLocales()
	locales[0] = "changed"
	if SupportedLocales()[0] == "changed" || CatalogVersion() == "" {
		t.Fatal("catalog metadata was not isolated")
	}
	key := "backend.invalid_json"
	value, ok := Lookup("en", key)
	if !ok || value == "" || T("en-US", key) != value {
		t.Fatalf("lookup value=%q ok=%v", value, ok)
	}
	if _, ok := Lookup("unsupported", key); ok {
		t.Fatal("unsupported locale lookup succeeded")
	}
	if _, ok := Lookup("en-US", "missing.key"); ok {
		t.Fatal("missing key lookup succeeded")
	}
	payload, ok := Resources("en-US", "backend")
	if !ok || payload.Locale != "en-US" || payload.DefaultLocale != DefaultLocale || payload.CatalogVersion == "" || len(payload.Resources) == 0 {
		t.Fatalf("payload=%+v ok=%v", payload, ok)
	}
	exact, ok := Resources("en-US", key)
	if !ok || len(exact.Resources) != 1 || exact.Resources[key] == "" {
		t.Fatalf("exact resources=%+v ok=%v", exact, ok)
	}
	all, ok := Resources("en-US", " . ")
	if !ok || len(all.Resources) < len(payload.Resources) {
		t.Fatalf("all resources=%d backend resources=%d", len(all.Resources), len(payload.Resources))
	}
	if _, ok := Resources("unsupported", ""); ok {
		t.Fatal("unsupported resource locale succeeded")
	}
	formattedCatalog := Catalog{DefaultLocale: DefaultLocale, Locales: []string{DefaultLocale}, CatalogVersion: "test", Resources: map[string]map[string]string{DefaultLocale: {"welcome": "Hello {{name}}, {{name}}!"}}}
	original := catalogs
	catalogs = formattedCatalog
	t.Cleanup(func() { catalogs = original })
	if Format("en-US", "welcome", map[string]string{"name": "Ada"}) != "Hello Ada, Ada!" {
		t.Fatal("message formatting failed")
	}
	for _, call := range []func(){
		func() { T("unsupported", "welcome") },
		func() { T("en-US", "missing") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("catalog contract violation did not panic")
				}
			}()
			call()
		}()
	}
}

func TestParseCatalogTOMLSectionsEscapesDuplicatesAndErrors(t *testing.T) {
	source := `
# comment
"root" = "Root"
[messages]
"root" = "Ignored duplicate for compatibility"
"escaped" = "line\nvalue"
[auth]
"login" = "Log in"
`
	values, err := parseCatalogTOML(source)
	if err != nil || values["root"] != "Root" || values["escaped"] != "line\nvalue" || values["auth.login"] != "Log in" {
		t.Fatalf("values=%v err=%v", values, err)
	}
	invalid := []string{
		`[]`,
		`[auth`,
		`plain = "value"`,
		`"key" "value"`,
		`"key" = plain`,
		`"key" = "value" trailing`,
		`"bad\x" = "value"`,
		`"key" = "bad\x"`,
		"[auth]\n\"key\" = \"one\"\n\"key\" = \"two\"",
	}
	for _, source := range invalid {
		if _, err := parseCatalogTOML(source); err == nil {
			t.Fatalf("invalid TOML accepted: %q", source)
		}
	}
	for _, test := range []struct {
		input   string
		literal string
		rest    string
		ok      bool
	}{
		{input: ` "a\\\"b" tail`, literal: `"a\\\"b"`, rest: " tail", ok: true},
		{input: "plain", rest: "plain"},
		{input: `"unterminated`, rest: `"unterminated`},
	} {
		literal, rest, ok := scanQuotedLiteral(test.input)
		if literal != test.literal || rest != test.rest || ok != test.ok {
			t.Fatalf("scan(%q)=(%q,%q,%v) want (%q,%q,%v)", test.input, literal, rest, ok, test.literal, test.rest, test.ok)
		}
	}
}

func TestLoadCatalogFilesAndDirectoryFailureMatrix(t *testing.T) {
	valid := fstest.MapFS{
		"locales/en-US.toml":       &fstest.MapFile{Data: []byte(`"hello" = "Hello"`)},
		"locales/zh-CN.toml":       &fstest.MapFile{Data: []byte(`"hello" = "你好"`)},
		"locales/README.txt":       &fstest.MapFile{Data: []byte("ignored")},
		"locales/nested/file.toml": &fstest.MapFile{Data: []byte(`"ignored" = "ignored"`)},
	}
	catalog, err := loadCatalogFromFS(valid, "locales")
	if err != nil || len(catalog.Locales) != 2 || catalog.Locales[0] != "en-US" || catalog.Resources["zh-CN"]["hello"] != "你好" || catalog.CatalogVersion == "" {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	if _, err := loadCatalogFromFS(valid, "missing"); err == nil {
		t.Fatal("missing embedded directory accepted")
	}
	readFailure := catalogReadFailureFS{FS: fstest.MapFS{"locales/en-US.toml": &fstest.MapFile{Data: []byte(`"hello" = "Hello"`)}}}
	if _, err := loadCatalogFromFS(readFailure, "locales"); err == nil {
		t.Fatal("unreadable locale file accepted")
	}
	for name, files := range map[string]fs.FS{
		"empty":           fstest.MapFS{"locales/README": &fstest.MapFile{Data: []byte("ignored")}},
		"missing default": fstest.MapFS{"locales/zh-CN.toml": &fstest.MapFile{Data: []byte(`"hello" = "你好"`)}},
		"invalid TOML":    fstest.MapFS{"locales/en-US.toml": &fstest.MapFile{Data: []byte("invalid")}},
	} {
		if _, err := loadCatalogFiles(files, "locales", name); err == nil {
			t.Fatalf("%s catalog accepted", name)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en-US.toml"), []byte(`"hello" = "Hello"`), 0o600); err != nil {
		t.Fatal(err)
	}
	fromDir, err := loadCatalogFromDir(dir)
	if err != nil || fromDir.Resources["en-US"]["hello"] != "Hello" {
		t.Fatalf("directory catalog=%+v err=%v", fromDir, err)
	}
	t.Setenv("APP_I18N_DIR", " "+dir+" ")
	if candidates := localeDirCandidates(); len(candidates) != 1 || candidates[0] != dir {
		t.Fatalf("environment candidates=%v", candidates)
	}
	t.Setenv("APP_I18N_DIR", "")
	if candidates := localeDirCandidates(); len(candidates) < 9 || !strings.Contains(strings.Join(candidates, "\n"), "locales") {
		t.Fatalf("fallback candidates=%v", candidates)
	}
}

func TestLoadCatalogFallsBackToConfiguredDirectoryAndPanicsWhenUnavailable(t *testing.T) {
	originalFS := embeddedCatalogFS
	embeddedCatalogFS = fstest.MapFS{}
	t.Cleanup(func() { embeddedCatalogFS = originalFS })

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en-US.toml"), []byte(`"hello" = "Hello"`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_I18N_DIR", dir)
	if catalog := loadCatalog(); catalog.Resources[DefaultLocale]["hello"] != "Hello" {
		t.Fatalf("fallback catalog = %+v", catalog)
	}

	t.Setenv("APP_I18N_DIR", filepath.Join(dir, "missing"))
	defer func() {
		if recover() == nil {
			t.Fatal("unavailable catalogs did not panic")
		}
	}()
	loadCatalog()
}

func TestLocaleDirCandidatesHandlesUnavailableCallerMetadata(t *testing.T) {
	originalCaller := localeRuntimeCaller
	localeRuntimeCaller = func(int) (uintptr, string, int, bool) { return 0, "", 0, false }
	t.Cleanup(func() { localeRuntimeCaller = originalCaller })
	t.Setenv("APP_I18N_DIR", "")

	if candidates := localeDirCandidates(); len(candidates) != 9 {
		t.Fatalf("fallback candidates without caller metadata = %v", candidates)
	}
}

func TestProjectCatalogExtensionOverridesKeysAndPreservesRuntimeDefaults(t *testing.T) {
	dir := t.TempDir()
	originalVersion := CatalogVersion()
	if err := os.WriteFile(filepath.Join(dir, "zh-CN.toml"), []byte("[auth]\n\"login\" = \"项目账号\"\n[project]\n\"welcome\" = \"欢迎进入项目\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureProjectExtension("") })
	if err := ConfigureProjectExtension(dir); err != nil {
		t.Fatal(err)
	}
	payload, ok := Resources("zh-CN", "auth")
	if !ok || payload.Resources["auth.login"] != "项目账号" || payload.Resources["auth.password"] == "" {
		t.Fatalf("merged resources=%+v ok=%v", payload, ok)
	}
	project, ok := Resources("zh-CN", "project")
	if !ok || project.Resources["project.welcome"] != "欢迎进入项目" {
		t.Fatalf("project resources=%+v ok=%v", project, ok)
	}
	if value, _ := Lookup("en-US", "auth.login"); value != "Account / email" {
		t.Fatalf("unrelated locale changed: %q", value)
	}
	if CatalogVersion() == originalVersion || payload.CatalogVersion != CatalogVersion() {
		t.Fatalf("extension catalog identity was not published")
	}
}

func TestProjectCatalogExtensionMissingEmptyAndUnsafeCases(t *testing.T) {
	originalVersion := CatalogVersion()
	t.Cleanup(func() { _ = ConfigureProjectExtension("") })
	if err := ConfigureProjectExtension(filepath.Join(t.TempDir(), "missing")); err != nil || CatalogVersion() != originalVersion {
		t.Fatalf("missing extension changed defaults: version=%s err=%v", CatalogVersion(), err)
	}
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "en-US.toml"), []byte("# no overrides\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureProjectExtension(empty); err != nil || CatalogVersion() != originalVersion {
		t.Fatalf("empty extension changed defaults: version=%s err=%v", CatalogVersion(), err)
	}
	unsupported := t.TempDir()
	if err := os.WriteFile(filepath.Join(unsupported, "xx-YY.toml"), []byte("\"key\" = \"value\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureProjectExtension(unsupported); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unsupported locale error=%v", err)
	}
	symlinkParent := t.TempDir()
	symlink := filepath.Join(symlinkParent, "i18n")
	if err := os.Symlink(empty, symlink); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureProjectExtension(symlink); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("symlink extension error=%v", err)
	}
}

func TestProjectCatalogExtensionFilesystemBoundaryMatrix(t *testing.T) {
	t.Run("non-directory root", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "i18n")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadProjectCatalogExtension(path); err == nil {
			t.Fatal("file root was accepted")
		}
	})

	t.Run("read directory failure", func(t *testing.T) {
		previous := projectCatalogReadDir
		projectCatalogReadDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("read dir failed") }
		t.Cleanup(func() { projectCatalogReadDir = previous })
		if _, err := loadProjectCatalogExtension(t.TempDir()); err == nil {
			t.Fatal("read directory failure was ignored")
		}
	})

	t.Run("locale lstat and type failures", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "en-US.toml")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		previous := projectCatalogLstat
		projectCatalogLstat = func(candidate string) (os.FileInfo, error) {
			if candidate == path {
				return nil, errors.New("lstat failed")
			}
			return os.Lstat(candidate)
		}
		if _, err := loadProjectCatalogExtension(dir); err == nil {
			t.Fatal("locale lstat failure was ignored")
		}
		projectCatalogLstat = func(candidate string) (os.FileInfo, error) {
			if candidate == path {
				return os.Lstat(dir)
			}
			return os.Lstat(candidate)
		}
		if _, err := loadProjectCatalogExtension(dir); err == nil {
			t.Fatal("non-regular locale was accepted")
		}
		projectCatalogLstat = previous
	})

	t.Run("aggregate size is unlimited", func(t *testing.T) {
		dir := t.TempDir()
		locales := SupportedLocales()
		for index := 0; index < 5; index++ {
			if err := os.WriteFile(filepath.Join(dir, locales[index]+".toml"), []byte(strings.Repeat("# padding\n", (220<<10)/10)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		values, err := loadProjectCatalogExtension(dir)
		if err != nil || len(values) != 5 {
			t.Fatalf("aggregate extension values=%d error=%v", len(values), err)
		}
	})

	t.Run("directory entry limit", func(t *testing.T) {
		dir := t.TempDir()
		for index := 0; index < 33; index++ {
			path := filepath.Join(dir, fmt.Sprintf("ignored-%02d.txt", index))
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := loadProjectCatalogExtension(dir); err == nil || !strings.Contains(err.Error(), "32 directory entries") {
			t.Fatalf("entry-limit error=%v", err)
		}
	})

	t.Run("directories and non TOML files are ignored", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "nested.toml"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignored"), 0o600); err != nil {
			t.Fatal(err)
		}
		values, err := loadProjectCatalogExtension(dir)
		if err != nil || len(values) != 0 {
			t.Fatalf("ignored entries values=%v error=%v", values, err)
		}
	})

	t.Run("locale file symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "en-US.toml")
		if err := os.WriteFile(target, []byte(`"key" = "value"`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "en-US.toml")); err != nil {
			t.Fatal(err)
		}
		if _, err := loadProjectCatalogExtension(dir); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("symlink file error=%v", err)
		}
	})

	t.Run("single file size is unlimited", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "en-US.toml")
		largeValue := strings.Repeat("x", (256<<10)+1)
		if err := os.WriteFile(path, []byte(`"project.large" = "`+largeValue+`"`), 0o600); err != nil {
			t.Fatal(err)
		}
		values, err := loadProjectCatalogExtension(dir)
		if err != nil || values["en-US"]["project.large"] != largeValue {
			t.Fatalf("large extension value bytes=%d error=%v", len(values["en-US"]["project.large"]), err)
		}
	})

	t.Run("qg sized locale", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "pt-BR.toml")
		writeCatalogFixtureSize(t, path, 267873, 3572)
		values, err := loadProjectCatalogExtension(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(values["pt-BR"]) != 3572 {
			t.Fatalf("qg-sized locale keys=%d", len(values["pt-BR"]))
		}
	})

	t.Run("normalized locale duplicate", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"en.toml", "en-US.toml"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(`"key" = "value"`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := loadProjectCatalogExtension(dir); err == nil || !strings.Contains(err.Error(), "duplicates locale") {
			t.Fatalf("duplicate-locale error=%v", err)
		}
	})

	t.Run("invalid TOML", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "en-US.toml"), []byte("invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadProjectCatalogExtension(dir); err == nil || !strings.Contains(err.Error(), "en-US.toml") {
			t.Fatalf("invalid TOML error=%v", err)
		}
	})

	t.Run("read file failure", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "en-US.toml")
		if err := os.WriteFile(path, []byte(`"key" = "value"`), 0o600); err != nil {
			t.Fatal(err)
		}
		previous := projectCatalogReadFile
		projectCatalogReadFile = func(candidate string) ([]byte, error) {
			if candidate == path {
				return nil, errors.New("read file failed")
			}
			return os.ReadFile(candidate)
		}
		t.Cleanup(func() { projectCatalogReadFile = previous })
		if _, err := loadProjectCatalogExtension(dir); err == nil || !strings.Contains(err.Error(), "read file failed") {
			t.Fatalf("read file error=%v", err)
		}
	})
}

func writeCatalogFixtureSize(t *testing.T, path string, size, keyCount int) {
	t.Helper()
	var content strings.Builder
	for index := 0; index < keyCount; index++ {
		_, _ = fmt.Fprintf(&content, `"project.capacity.key_%04d" = "value %04d"`+"\n", index, index)
	}
	if content.Len()+2 > size {
		t.Fatalf("catalog fixture content %d exceeds requested size %d", content.Len(), size)
	}
	padding := size - content.Len()
	content.WriteByte('#')
	content.WriteString(strings.Repeat("x", padding-1))
	if content.Len() != size {
		t.Fatalf("catalog fixture size=%d want=%d", content.Len(), size)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
