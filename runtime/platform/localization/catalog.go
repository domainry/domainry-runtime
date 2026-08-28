package localization

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	embeddedlocales "github.com/domainry/domainry-runtime/runtime/platform/localization/locales"
)

const DefaultLocale = "en-US"

const (
	// projectCatalogLocaleFileMaxBytes allows a complete project-owned locale
	// catalog while bounding the bytes parsed from any one regular file.
	projectCatalogLocaleFileMaxBytes int64 = 512 << 10
	// projectCatalogTotalMaxBytes bounds all project-owned locale content loaded
	// into one Runtime process. Raising the per-locale limit does not change it.
	projectCatalogTotalMaxBytes int64 = 1 << 20
	projectCatalogMaxEntries          = 32
)

type Catalog struct {
	DefaultLocale  string                       `json:"default_locale"`
	Locales        []string                     `json:"locales"`
	CatalogVersion string                       `json:"catalog_version"`
	Resources      map[string]map[string]string `json:"-"`
}

type ResourcesPayload struct {
	Locale         string            `json:"locale"`
	DefaultLocale  string            `json:"default_locale"`
	CatalogVersion string            `json:"catalog_version"`
	Resources      map[string]string `json:"resources"`
}

var catalogs = loadCatalog()
var embeddedCatalogFS fs.FS = embeddedlocales.Files
var localeRuntimeCaller = runtime.Caller
var projectCatalogMu sync.RWMutex
var projectCatalog *Catalog

// ConfigureProjectExtension overlays project-owned locale files over the
// embedded Runtime catalog. A missing directory means the project accepts all
// Runtime defaults. Only already-supported locales may be extended.
func ConfigureProjectExtension(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		projectCatalogMu.Lock()
		projectCatalog = nil
		projectCatalogMu.Unlock()
		return nil
	}
	extension, err := loadProjectCatalogExtension(dir)
	if errors.Is(err, os.ErrNotExist) {
		extension = map[string]map[string]string{}
		err = nil
	}
	if err != nil {
		return err
	}
	hasValues := false
	for _, values := range extension {
		hasValues = hasValues || len(values) > 0
	}
	if !hasValues {
		projectCatalogMu.Lock()
		projectCatalog = nil
		projectCatalogMu.Unlock()
		return nil
	}
	merged := mergeCatalogExtension(catalogs, extension)
	projectCatalogMu.Lock()
	projectCatalog = &merged
	projectCatalogMu.Unlock()
	return nil
}

func activeCatalog() Catalog {
	projectCatalogMu.RLock()
	defer projectCatalogMu.RUnlock()
	if projectCatalog != nil {
		return *projectCatalog
	}
	return catalogs
}

func NormalizeLocale(locale string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(locale), "_", "-"))
	switch normalized {
	case "":
		return DefaultLocale
	case "zh", "zh-cn", "zh-hans", "zh-hans-cn", "cn":
		return "zh-CN"
	case "zh-tw", "zh-hant", "zh-hant-tw", "tw":
		return "zh-TW"
	case "en", "en-us", "en-gb":
		return "en-US"
	case "ja", "ja-jp", "jp":
		return "ja-JP"
	case "pt", "pt-br", "br":
		return "pt-BR"
	case "ko", "ko-kr", "kr":
		return "ko-KR"
	case "de", "de-de":
		return "de-DE"
	case "it", "it-it":
		return "it-IT"
	case "ar", "ar-sa", "sa":
		return "ar-SA"
	case "fr", "fr-fr":
		return "fr-FR"
	case "es", "es-es":
		return "es-ES"
	case "tr", "tr-tr":
		return "tr-TR"
	default:
		return strings.TrimSpace(locale)
	}
}

func SupportedLocales() []string {
	return append([]string(nil), activeCatalog().Locales...)
}

func CatalogVersion() string {
	return activeCatalog().CatalogVersion
}

func T(locale string, key string) string {
	locale = NormalizeLocale(locale)
	catalog := activeCatalog()
	values, ok := catalog.Resources[locale]
	if !ok {
		panic(fmt.Sprintf("unsupported locale %q", locale))
	}
	value, ok := values[key]
	if !ok {
		panic(fmt.Sprintf("missing i18n key %q for locale %q", key, locale))
	}
	return value
}

func Format(locale string, key string, values map[string]string) string {
	message := T(locale, key)
	for name, value := range values {
		message = strings.ReplaceAll(message, "{{"+name+"}}", value)
	}
	return message
}

func Lookup(locale string, key string) (string, bool) {
	locale = NormalizeLocale(locale)
	values, ok := activeCatalog().Resources[locale]
	if !ok {
		return "", false
	}
	value, ok := values[key]
	return value, ok
}

func Resources(locale string, namespace string) (ResourcesPayload, bool) {
	locale = NormalizeLocale(locale)
	catalog := activeCatalog()
	values, ok := catalog.Resources[locale]
	if !ok {
		return ResourcesPayload{}, false
	}
	out := map[string]string{}
	namespace = strings.Trim(strings.TrimSpace(namespace), ".")
	for key, value := range values {
		if namespace == "" || key == namespace || strings.HasPrefix(key, namespace+".") {
			out[key] = value
		}
	}
	return ResourcesPayload{
		Locale:         locale,
		DefaultLocale:  catalog.DefaultLocale,
		CatalogVersion: catalog.CatalogVersion,
		Resources:      out,
	}, true
}

var projectCatalogLstat = os.Lstat
var projectCatalogReadDir = os.ReadDir
var projectCatalogReadFile = os.ReadFile

func loadProjectCatalogExtension(dir string) (map[string]map[string]string, error) {
	info, err := projectCatalogLstat(dir)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("project i18n extension must be a real directory: %s", dir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project i18n extension must be a real directory: %s", dir)
	}
	entries, err := projectCatalogReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) > projectCatalogMaxEntries {
		return nil, fmt.Errorf("project i18n extension exceeds 32 directory entries")
	}
	result := map[string]map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fileInfo, err := projectCatalogLstat(path)
		if err != nil {
			return nil, fmt.Errorf("project i18n extension %s must be a regular non-symlink file", entry.Name())
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("project i18n extension %s must be a regular non-symlink file", entry.Name())
		}
		if !fileInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("project i18n extension %s must be a regular non-symlink file", entry.Name())
		}
		locale := NormalizeLocale(strings.TrimSuffix(entry.Name(), ".toml"))
		if _, supported := catalogs.Resources[locale]; !supported {
			return nil, fmt.Errorf("project i18n extension locale %s is not supported by Runtime", locale)
		}
		if _, duplicate := result[locale]; duplicate {
			return nil, fmt.Errorf("project i18n extension duplicates locale %s", locale)
		}
		raw, err := projectCatalogReadFile(path)
		if err != nil {
			return nil, err
		}
		values, err := parseCatalogTOML(string(raw))
		if err != nil {
			return nil, fmt.Errorf("project i18n extension %s: %w", entry.Name(), err)
		}
		result[locale] = values
	}
	return result, nil
}

func mergeCatalogExtension(base Catalog, extension map[string]map[string]string) Catalog {
	resources := make(map[string]map[string]string, len(base.Resources))
	versionInput := strings.Builder{}
	versionInput.WriteString(base.CatalogVersion)
	versionInput.WriteByte('\n')
	for _, locale := range base.Locales {
		values := make(map[string]string, len(base.Resources[locale])+len(extension[locale]))
		for key, value := range base.Resources[locale] {
			values[key] = value
		}
		keys := make([]string, 0, len(extension[locale]))
		for key, value := range extension[locale] {
			values[key] = value
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			versionInput.WriteString(locale)
			versionInput.WriteByte(0)
			versionInput.WriteString(key)
			versionInput.WriteByte(0)
			versionInput.WriteString(extension[locale][key])
			versionInput.WriteByte(0)
		}
		resources[locale] = values
	}
	sum := sha256.Sum256([]byte(versionInput.String()))
	return Catalog{DefaultLocale: base.DefaultLocale, Locales: append([]string(nil), base.Locales...), CatalogVersion: hex.EncodeToString(sum[:]), Resources: resources}
}

func loadCatalog() Catalog {
	if embedded, err := loadCatalogFromFS(embeddedCatalogFS, "."); err == nil {
		return embedded
	}
	var failures []string
	for _, dir := range localeDirCandidates() {
		next, err := loadCatalogFromDir(dir)
		if err == nil {
			return next
		}
		failures = append(failures, dir+": "+err.Error())
	}
	panic("no backend locale TOML directory found or loadable; expected backend/conf/locales; checked " + strings.Join(failures, " | "))
}

func loadCatalogFromDir(dir string) (Catalog, error) {
	return loadCatalogFiles(os.DirFS(dir), ".", dir)
}

func loadCatalogFromFS(files fs.FS, dir string) (Catalog, error) {
	return loadCatalogFiles(files, dir, "embedded Runtime locale catalog")
}

func loadCatalogFiles(files fs.FS, dir string, label string) (Catalog, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return Catalog{}, err
	}
	resources := map[string]map[string]string{}
	versionInput := strings.Builder{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		locale := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		locale = NormalizeLocale(locale)
		raw, err := fs.ReadFile(files, filepath.ToSlash(filepath.Join(dir, entry.Name())))
		if err != nil {
			return Catalog{}, err
		}
		values, err := parseCatalogTOML(string(raw))
		if err != nil {
			return Catalog{}, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		resources[locale] = values
		versionInput.WriteString(locale)
		versionInput.WriteString("\n")
		versionInput.Write(raw)
		versionInput.WriteString("\n")
	}
	if len(resources) == 0 {
		return Catalog{}, fmt.Errorf("no locale TOML files in %s", label)
	}
	if _, ok := resources[DefaultLocale]; !ok {
		return Catalog{}, fmt.Errorf("default locale %s is missing in %s", DefaultLocale, label)
	}
	locales := make([]string, 0, len(resources))
	for locale := range resources {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	sum := sha256.Sum256([]byte(versionInput.String()))
	return Catalog{
		DefaultLocale:  DefaultLocale,
		Locales:        locales,
		CatalogVersion: hex.EncodeToString(sum[:]),
		Resources:      resources,
	}, nil
}

func parseCatalogTOML(source string) (map[string]string, error) {
	out := map[string]string{}
	section := ""
	for lineNumber, rawLine := range strings.Split(source, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			if section == "" {
				return nil, fmt.Errorf("line %d has empty section", lineNumber+1)
			}
			continue
		}
		keyLiteral, rest, ok := scanQuotedLiteral(line)
		if !ok {
			return nil, fmt.Errorf("line %d must start with a quoted key", lineNumber+1)
		}
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "=") {
			return nil, fmt.Errorf("line %d is missing =", lineNumber+1)
		}
		valueLiteral, trailing, ok := scanQuotedLiteral(strings.TrimSpace(strings.TrimPrefix(rest, "=")))
		if !ok {
			return nil, fmt.Errorf("line %d must contain a quoted string value", lineNumber+1)
		}
		if strings.TrimSpace(trailing) != "" {
			return nil, fmt.Errorf("line %d has unsupported trailing content", lineNumber+1)
		}
		key, err := strconv.Unquote(keyLiteral)
		if err != nil {
			return nil, fmt.Errorf("line %d has invalid key: %w", lineNumber+1, err)
		}
		value, err := strconv.Unquote(valueLiteral)
		if err != nil {
			return nil, fmt.Errorf("line %d has invalid value: %w", lineNumber+1, err)
		}
		if section != "" && section != "messages" {
			key = section + "." + key
		}
		if _, exists := out[key]; exists {
			if section == "messages" {
				continue
			}
			return nil, fmt.Errorf("line %d duplicates key %q", lineNumber+1, key)
		}
		out[key] = value
	}
	return out, nil
}

func scanQuotedLiteral(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, `"`) {
		return "", value, false
	}
	escaped := false
	for index := 1; index < len(value); index++ {
		ch := value[index]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return value[:index+1], value[index+1:], true
		}
	}
	return "", value, false
}

func localeDirCandidates() []string {
	if dir := strings.TrimSpace(os.Getenv("APP_I18N_DIR")); dir != "" {
		return []string{dir}
	}
	candidates := []string{
		filepath.Join("runtime", "platform", "localization", "locales"),
		filepath.Join("runtime", "conf", "locales"),
		filepath.Join("conf", "locales"),
		filepath.Join("backend", "conf", "locales"),
		filepath.Join("..", "backend", "conf", "locales"),
		filepath.Join("..", "conf", "locales"),
		filepath.Join("..", "..", "conf", "locales"),
		filepath.Join("..", "..", "..", "..", "conf", "locales"),
		filepath.Join("..", "..", "..", "backend", "conf", "locales"),
	}
	if _, file, _, ok := localeRuntimeCaller(0); ok {
		sourceDir := filepath.Dir(file)
		candidates = append(candidates,
			filepath.Join(sourceDir, "locales"),
			filepath.Join(sourceDir, "..", "conf", "locales"),
			filepath.Join(sourceDir, "..", "..", "conf", "locales"),
			filepath.Join(sourceDir, "..", "..", "..", "backend", "conf", "locales"),
		)
	}
	return candidates
}
