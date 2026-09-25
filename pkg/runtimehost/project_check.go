package runtimehost

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	projectvalidation "github.com/domainry/domainry-runtime/runtime/domain/project/validation"
)

const (
	projectCheckStateValid   = "valid"
	projectCheckStateInvalid = "invalid"
)

// ProjectCheckDiagnostic is one stable, machine-readable problem found while
// preparing a project model and its code-owned definition registry. Paths are
// JSON pointers for model-owned problems and definition registry paths for
// code-owned problems.
type ProjectCheckDiagnostic struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Path        string `json:"path"`
	Message     string `json:"message"`
	Owner       string `json:"owner"`
	Category    string `json:"category"`
	Remediation string `json:"remediation,omitempty"`
}

// ProjectCheckResult is the side-effect-free result of checking the one
// project model and the frozen Go definitions supplied through Options.
type ProjectCheckResult struct {
	State            string                   `json:"state"`
	ModelContentHash string                   `json:"modelContentHash,omitempty"`
	Diagnostics      []ProjectCheckDiagnostic `json:"diagnostics"`
}

// CheckProject validates and compiles the project model together with its
// code-owned definitions without loading Runtime configuration, opening a
// database, starting workers, binding listeners, or constructing module
// factories.
func CheckProject(options Options) ProjectCheckResult {
	checked, err := prepareCheckedProject(options, os.ReadFile)
	return projectCheckResult(checked, err)
}

// CheckModel validates and compiles only the JSON-owned project model. It is
// the pre-backend gate: references to code-owned actions remain deferred until
// CheckProject can close them against the frozen Go definition registry.
func CheckModel(options Options) ProjectCheckResult {
	checked, err := prepareCheckedModel(options, os.ReadFile)
	return projectCheckResult(checked, err)
}

func projectCheckResult(checked checkedProject, err error) ProjectCheckResult {
	if err != nil {
		return ProjectCheckResult{
			State:       projectCheckStateInvalid,
			Diagnostics: projectCheckDiagnostics(err),
		}
	}
	return ProjectCheckResult{
		State:            projectCheckStateValid,
		ModelContentHash: checked.runtimeModel.ContentHash,
		Diagnostics:      []ProjectCheckDiagnostic{},
	}
}

type checkedProject struct {
	extensions       *runtimeext.ProjectExtensionRegistry
	connectorGateway *bindableConnectorGateway
	runtimeModel     projectmodel.RuntimeModel
}

type projectCheckBoundaryError struct {
	code        string
	path        string
	message     string
	owner       string
	category    string
	remediation string
	cause       error
}

func (e *projectCheckBoundaryError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}

func (e *projectCheckBoundaryError) Unwrap() error { return e.cause }

func prepareCheckedProject(options Options, readFile func(string) ([]byte, error)) (checkedProject, error) {
	projectExtensions, connectorGateway, err := prepareProjectExtensions(options)
	if err != nil {
		return checkedProject{}, &projectCheckBoundaryError{
			code: "runtime_project.composition_invalid", path: "/definitions", message: "validate project Runtime composition",
			owner: "project_code", category: "composition", remediation: "fix the project build identity or code-owned definition registry", cause: err,
		}
	}
	return prepareProjectModel(options, readFile, projectExtensions, connectorGateway)
}

func prepareCheckedModel(options Options, readFile func(string) ([]byte, error)) (checkedProject, error) {
	return prepareProjectModel(options, readFile, nil, nil)
}

func prepareProjectModel(
	options Options,
	readFile func(string) ([]byte, error),
	projectExtensions *runtimeext.ProjectExtensionRegistry,
	connectorGateway *bindableConnectorGateway,
) (checkedProject, error) {
	modelFile := strings.TrimSpace(options.ModelFile)
	if modelFile == "" {
		return checkedProject{}, &projectCheckBoundaryError{
			code: "runtime_project.model_file_required", path: "/modelFile", message: "ModelFile is required",
			owner: "runtime_host", category: "configuration", remediation: "set Options.ModelFile to backend/model.json",
		}
	}
	if readFile == nil {
		return checkedProject{}, &projectCheckBoundaryError{
			code: "runtime_project.model_reader_unavailable", path: "/modelFile", message: "project model reader is unavailable",
			owner: "runtime_host", category: "configuration",
		}
	}
	rawModel, err := readFile(modelFile)
	if err != nil {
		return checkedProject{}, &projectCheckBoundaryError{
			code: "runtime_project.model_file_unreadable", path: "/modelFile", message: fmt.Sprintf("read project model %s", modelFile),
			owner: "project_model", category: "io", remediation: "ensure backend/model.json exists and is readable", cause: err,
		}
	}
	projectModel, err := projectmodel.Decode(rawModel)
	if err != nil {
		return checkedProject{}, err
	}
	if projectExtensions == nil {
		if err := projectmodel.Validate(projectModel, nil); err != nil {
			return checkedProject{}, err
		}
	} else {
		if err := projectvalidation.ValidateComposition(projectModel, projectExtensions, nil); err != nil {
			return checkedProject{}, err
		}
	}
	runtimeModel, err := projectmodel.Compile(projectModel)
	if err != nil {
		return checkedProject{}, err
	}
	if projectExtensions != nil {
		if err := attachPublicResources(&runtimeModel, projectExtensions.ProjectDefinitions().PublicResources); err != nil {
			return checkedProject{}, &projectCheckBoundaryError{
				code: "project_definition.public_resources_invalid", path: "/definitions/public_resources", message: "attach code-owned public resources",
				owner: "project_code", category: "composition", remediation: "fix the code-owned public resource definitions", cause: err,
			}
		}
	}
	return checkedProject{extensions: projectExtensions, connectorGateway: connectorGateway, runtimeModel: runtimeModel}, nil
}

func projectCheckDiagnostics(err error) []ProjectCheckDiagnostic {
	var validationError *projectmodel.ValidationError
	if errors.As(err, &validationError) {
		diagnostics := make([]ProjectCheckDiagnostic, 0, len(validationError.Issues))
		for _, issue := range validationError.Issues {
			owner, category := projectIssueOwnerAndCategory(issue.Code)
			diagnostics = append(diagnostics, ProjectCheckDiagnostic{
				Code: issue.Code, Severity: "error", Path: issue.Pointer, Message: issue.Message,
				Owner: owner, Category: category,
			})
		}
		return diagnostics
	}
	var decodeError *projectmodel.DecodeError
	if errors.As(err, &decodeError) {
		path := decodeError.Pointer
		if path == "" {
			path = "/"
		}
		message := decodeError.Message
		if decodeError.Cause != nil {
			message = fmt.Sprintf("%s: %v", message, decodeError.Cause)
		}
		return []ProjectCheckDiagnostic{{
			Code: decodeError.Code, Severity: "error", Path: path, Message: message,
			Owner: "project_model", Category: "schema",
		}}
	}
	var boundaryError *projectCheckBoundaryError
	if errors.As(err, &boundaryError) {
		return []ProjectCheckDiagnostic{{
			Code: boundaryError.code, Severity: "error", Path: boundaryError.path, Message: boundaryError.Error(),
			Owner: boundaryError.owner, Category: boundaryError.category, Remediation: boundaryError.remediation,
		}}
	}
	return []ProjectCheckDiagnostic{{
		Code: "runtime_project.check_failed", Severity: "error", Path: "/", Message: err.Error(),
		Owner: "runtime_host", Category: "internal",
	}}
}

func projectIssueOwnerAndCategory(code string) (string, string) {
	if strings.HasPrefix(code, "project_definition.") {
		return "project_code", "composition"
	}
	return "project_model", "semantic"
}
