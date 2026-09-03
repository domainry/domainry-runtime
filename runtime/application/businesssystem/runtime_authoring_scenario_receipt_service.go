package businesssystem

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

type RuntimeAuthoringScenarioStepObservation struct {
	SessionID           string
	StepID              string
	BuilderTaskID       string
	SnapshotHash        string
	CoverageHash        string
	ScenarioID          string
	Categories          []string
	Label               string
	Observation         string
	Method              string
	Path                string
	ExpectedStatus      []int
	ActualStatus        int
	RequestHash         string
	ResponseHash        string
	IdempotencyKey      string
	IdempotencyReplayed bool
}

type RuntimeAuthoringScenarioStepReceipt struct {
	Version             string   `json:"version"`
	SessionID           string   `json:"session_id,omitempty"`
	StepID              string   `json:"step_id,omitempty"`
	BuilderTaskID       string   `json:"builder_task_id"`
	SnapshotHash        string   `json:"snapshot_hash"`
	CoverageHash        string   `json:"coverage_hash"`
	ScenarioID          string   `json:"scenario_id"`
	Categories          []string `json:"categories"`
	Label               string   `json:"label"`
	Observation         string   `json:"observation,omitempty"`
	Method              string   `json:"method"`
	Path                string   `json:"path"`
	ExpectedStatus      []int    `json:"expected_status"`
	ActualStatus        int      `json:"actual_status"`
	RequestHash         string   `json:"request_hash"`
	ResponseHash        string   `json:"response_hash"`
	IdempotencyKey      string   `json:"idempotency_key,omitempty"`
	IdempotencyReplayed bool     `json:"idempotency_replayed,omitempty"`
}

type RuntimeAuthoringEvidenceStepClaims struct {
	Version        string   `json:"version"`
	SessionID      string   `json:"session_id"`
	BuilderTaskID  string   `json:"builder_task_id"`
	SnapshotHash   string   `json:"snapshot_hash"`
	CoverageHash   string   `json:"coverage_hash"`
	ScenarioID     string   `json:"scenario_id"`
	Categories     []string `json:"categories"`
	StepID         string   `json:"step_id"`
	Label          string   `json:"label"`
	Observation    string   `json:"observation,omitempty"`
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	ExpectedStatus []int    `json:"expected_status"`
}

// Token payloads are opaque Runtime-owned transport. Short keys, digest
// encoding, and a category bitset keep the model-facing tokens bounded while
// the application layer continues to consume descriptive typed claims.
type runtimeAuthoringEvidenceStepTokenPayload struct {
	SessionID     string `json:"s"`
	BuilderTaskID string `json:"b"`
	SnapshotHash  string `json:"n"`
	CoverageHash  string `json:"c"`
	ScenarioID    string `json:"q"`
	Categories    uint16 `json:"g"`
	StepID        string `json:"i"`
	Label         string `json:"l"`
	Observation   string `json:"o,omitempty"`
	Method        string `json:"m"`
	Path          string `json:"p"`
	Expected      []int  `json:"e"`
}

type runtimeAuthoringScenarioReceiptPayload struct {
	SessionID           string `json:"s,omitempty"`
	StepID              string `json:"i,omitempty"`
	BuilderTaskID       string `json:"b"`
	SnapshotHash        string `json:"n"`
	CoverageHash        string `json:"c"`
	ScenarioID          string `json:"q"`
	Categories          uint16 `json:"g"`
	Label               string `json:"l"`
	Observation         string `json:"o,omitempty"`
	Method              string `json:"m"`
	Path                string `json:"p"`
	Expected            []int  `json:"e"`
	Actual              int    `json:"a"`
	RequestHash         string `json:"r"`
	ResponseHash        string `json:"x"`
	IdempotencyKey      string `json:"k,omitempty"`
	IdempotencyReplayed bool   `json:"y,omitempty"`
}

type RuntimeAuthoringScenarioReceiptService struct {
	key []byte
}

func NewRuntimeAuthoringScenarioReceiptService(key []byte) *RuntimeAuthoringScenarioReceiptService {
	return &RuntimeAuthoringScenarioReceiptService{key: append([]byte(nil), key...)}
}

func runtimeAuthoringReceiptFromObservation(observation RuntimeAuthoringScenarioStepObservation) RuntimeAuthoringScenarioStepReceipt {
	return normalizeRuntimeAuthoringScenarioReceipt(RuntimeAuthoringScenarioStepReceipt{
		Version: changeplanmodel.RuntimeAuthoringStepReceiptVersion, SessionID: observation.SessionID, StepID: observation.StepID,
		BuilderTaskID: observation.BuilderTaskID, SnapshotHash: observation.SnapshotHash, CoverageHash: observation.CoverageHash,
		ScenarioID: observation.ScenarioID, Categories: observation.Categories, Label: observation.Label, Observation: observation.Observation,
		Method: observation.Method, Path: observation.Path, ExpectedStatus: observation.ExpectedStatus, ActualStatus: observation.ActualStatus,
		RequestHash: observation.RequestHash, ResponseHash: observation.ResponseHash,
		IdempotencyKey: observation.IdempotencyKey, IdempotencyReplayed: observation.IdempotencyReplayed,
	})
}

func (s *RuntimeAuthoringScenarioReceiptService) IssueEvidenceSession(builderTaskID string, binding changeplanmodel.RuntimeAuthoringEvidenceBinding, coverage changeplanmodel.RuntimeAuthoringCoverageLedger, plan changeplanmodel.RuntimeAuthoringEvidencePlan) (changeplanmodel.RuntimeAuthoringEvidenceSession, error) {
	builderTaskID = strings.TrimSpace(builderTaskID)
	if s == nil || len(s.key) < sha256.Size || builderTaskID == "" || !runtimeAuthoringReceiptSHA256(binding.SnapshotHash) || !runtimeAuthoringReceiptSHA256(binding.CoverageHash) {
		return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence session identity is invalid")
	}
	if binding.CoverageHash != runtimeAuthoringCoverageHash(&coverage) {
		return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan coverage binding is stale")
	}
	declaredScenarios := map[string]bool{}
	for _, requirement := range coverage.Requirements {
		for _, scenarioID := range requirement.ScenarioIDs {
			if scenarioID = strings.TrimSpace(scenarioID); scenarioID != "" {
				declaredScenarios[scenarioID] = true
			}
		}
	}
	normalizedPlan := changeplanmodel.RuntimeAuthoringEvidencePlan{Scenarios: []changeplanmodel.RuntimeAuthoringEvidenceScenarioPlan{}}
	seenScenarios, seenSteps, categorySet := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, scenario := range plan.Scenarios {
		scenario.ScenarioID = strings.TrimSpace(scenario.ScenarioID)
		if scenario.ScenarioID == "" || !declaredScenarios[scenario.ScenarioID] || seenScenarios[scenario.ScenarioID] {
			return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan scenario is invalid")
		}
		seenScenarios[scenario.ScenarioID] = true
		scenario.Categories = normalizedRuntimeAuthoringScenarioCategories(scenario.Categories)
		if len(scenario.Categories) == 0 || len(scenario.Steps) == 0 {
			return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan scenario is incomplete")
		}
		for _, category := range scenario.Categories {
			if !runtimeAuthoringScenarioCategoryAllowed(category) {
				return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan category is invalid")
			}
			categorySet[category] = true
		}
		normalizedScenario := changeplanmodel.RuntimeAuthoringEvidenceScenarioPlan{ScenarioID: scenario.ScenarioID, Categories: scenario.Categories, Steps: []changeplanmodel.RuntimeAuthoringEvidenceStepPlan{}}
		for _, step := range scenario.Steps {
			step.StepID, step.Label = strings.TrimSpace(step.StepID), strings.TrimSpace(step.Label)
			step.Observation, step.Method, step.Path = strings.TrimSpace(step.Observation), strings.ToUpper(strings.TrimSpace(step.Method)), strings.TrimSpace(step.Path)
			step.ExpectedStatus = normalizedRuntimeAuthoringExpectedStatuses(step.ExpectedStatus)
			if step.StepID == "" || seenSteps[step.StepID] || step.Label == "" || step.Method == "" || step.Path == "" || len(step.ExpectedStatus) == 0 {
				return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan step is incomplete")
			}
			if step.Observation != "" && step.Observation != "before_state" && step.Observation != "after_state" {
				return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan observation is invalid")
			}
			for _, status := range step.ExpectedStatus {
				if status < 100 || status > 599 {
					return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan expected status is invalid")
				}
			}
			seenSteps[step.StepID] = true
			normalizedScenario.Steps = append(normalizedScenario.Steps, step)
		}
		normalizedPlan.Scenarios = append(normalizedPlan.Scenarios, normalizedScenario)
	}
	for scenarioID := range declaredScenarios {
		if !seenScenarios[scenarioID] {
			return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan does not cover declared scenarios")
		}
	}
	for _, category := range changeplanmodel.RuntimeAuthoringRequiredScenarioCategories {
		if !categorySet[category] {
			return changeplanmodel.RuntimeAuthoringEvidenceSession{}, errors.New("runtime authoring evidence plan does not cover required categories")
		}
	}
	identity, _ := json.Marshal(struct {
		BuilderTaskID string                                          `json:"builder_task_id"`
		Binding       changeplanmodel.RuntimeAuthoringEvidenceBinding `json:"binding"`
		Plan          changeplanmodel.RuntimeAuthoringEvidencePlan    `json:"plan"`
	}{BuilderTaskID: builderTaskID, Binding: binding, Plan: normalizedPlan})
	sum := sha256.Sum256(identity)
	sessionID := hex.EncodeToString(sum[:])
	session := changeplanmodel.RuntimeAuthoringEvidenceSession{
		SessionID: sessionID, Steps: []changeplanmodel.RuntimeAuthoringEvidenceSessionStep{},
	}
	for _, scenario := range normalizedPlan.Scenarios {
		for _, step := range scenario.Steps {
			claims := RuntimeAuthoringEvidenceStepClaims{
				Version: changeplanmodel.RuntimeAuthoringEvidenceStepTokenVersion, SessionID: sessionID, BuilderTaskID: builderTaskID,
				SnapshotHash: binding.SnapshotHash, CoverageHash: binding.CoverageHash, ScenarioID: scenario.ScenarioID, Categories: scenario.Categories,
				StepID: step.StepID, Label: step.Label, Observation: step.Observation, Method: step.Method, Path: step.Path, ExpectedStatus: step.ExpectedStatus,
			}
			token, err := s.issueEvidenceStepToken(claims)
			if err != nil {
				return changeplanmodel.RuntimeAuthoringEvidenceSession{}, err
			}
			session.Steps = append(session.Steps, changeplanmodel.RuntimeAuthoringEvidenceSessionStep{StepID: step.StepID, Token: token})
		}
	}
	return session, nil
}

func (s *RuntimeAuthoringScenarioReceiptService) issueEvidenceStepToken(claims RuntimeAuthoringEvidenceStepClaims) (string, error) {
	payload, err := json.Marshal(runtimeAuthoringEvidenceStepTokenPayload{
		SessionID: claims.SessionID, BuilderTaskID: claims.BuilderTaskID,
		SnapshotHash: runtimeAuthoringEncodeDigest(claims.SnapshotHash), CoverageHash: runtimeAuthoringEncodeDigest(claims.CoverageHash),
		ScenarioID: claims.ScenarioID, Categories: runtimeAuthoringCategoryMask(claims.Categories),
		StepID: claims.StepID, Label: claims.Label, Observation: claims.Observation,
		Method: claims.Method, Path: claims.Path, Expected: claims.ExpectedStatus,
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return changeplanmodel.RuntimeAuthoringEvidenceStepTokenVersion + "." + encoded + "." + base64.RawURLEncoding.EncodeToString(s.sign(encoded)), nil
}

func (s *RuntimeAuthoringScenarioReceiptService) VerifyEvidenceStepToken(token string) (RuntimeAuthoringEvidenceStepClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[0] != changeplanmodel.RuntimeAuthoringEvidenceStepTokenVersion {
		return RuntimeAuthoringEvidenceStepClaims{}, errors.New("runtime authoring evidence step token format is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || s == nil || len(s.key) < sha256.Size || !hmac.Equal(signature, s.sign(parts[1])) {
		return RuntimeAuthoringEvidenceStepClaims{}, errors.New("runtime authoring evidence step token signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return RuntimeAuthoringEvidenceStepClaims{}, errors.New("runtime authoring evidence step token payload is invalid")
	}
	var wire runtimeAuthoringEvidenceStepTokenPayload
	if json.Unmarshal(payload, &wire) != nil {
		return RuntimeAuthoringEvidenceStepClaims{}, errors.New("runtime authoring evidence step token claims are invalid")
	}
	claims := RuntimeAuthoringEvidenceStepClaims{
		Version:   changeplanmodel.RuntimeAuthoringEvidenceStepTokenVersion,
		SessionID: wire.SessionID, BuilderTaskID: wire.BuilderTaskID,
		SnapshotHash: runtimeAuthoringDecodeDigest(wire.SnapshotHash), CoverageHash: runtimeAuthoringDecodeDigest(wire.CoverageHash),
		ScenarioID: wire.ScenarioID, Categories: runtimeAuthoringCategoriesFromMask(wire.Categories),
		StepID: wire.StepID, Label: wire.Label, Observation: wire.Observation,
		Method: wire.Method, Path: wire.Path, ExpectedStatus: wire.Expected,
	}
	if claims.SessionID == "" || claims.BuilderTaskID == "" || claims.ScenarioID == "" || claims.StepID == "" || claims.Label == "" || claims.Method == "" || claims.Path == "" || len(claims.Categories) == 0 || len(claims.ExpectedStatus) == 0 || !runtimeAuthoringReceiptSHA256(claims.SnapshotHash) || !runtimeAuthoringReceiptSHA256(claims.CoverageHash) {
		return RuntimeAuthoringEvidenceStepClaims{}, errors.New("runtime authoring evidence step token claims are invalid")
	}
	for _, category := range claims.Categories {
		if !runtimeAuthoringScenarioCategoryAllowed(category) {
			return RuntimeAuthoringEvidenceStepClaims{}, errors.New("runtime authoring evidence step token category is invalid")
		}
	}
	if claims.Observation != "" && claims.Observation != "before_state" && claims.Observation != "after_state" {
		return RuntimeAuthoringEvidenceStepClaims{}, errors.New("runtime authoring evidence step token observation is invalid")
	}
	for _, status := range claims.ExpectedStatus {
		if status < 100 || status > 599 {
			return RuntimeAuthoringEvidenceStepClaims{}, errors.New("runtime authoring evidence step token expected status is invalid")
		}
	}
	return claims, nil
}

func (s *RuntimeAuthoringScenarioReceiptService) ValidateObservation(observation RuntimeAuthoringScenarioStepObservation) error {
	return validateRuntimeAuthoringScenarioReceiptIdentity(s, runtimeAuthoringReceiptFromObservation(observation))
}

func (s *RuntimeAuthoringScenarioReceiptService) Issue(observation RuntimeAuthoringScenarioStepObservation) (string, error) {
	receipt := runtimeAuthoringReceiptFromObservation(observation)
	if err := validateRuntimeAuthoringScenarioReceipt(s, receipt); err != nil {
		return "", err
	}
	payload, err := json.Marshal(runtimeAuthoringScenarioReceiptPayload{
		SessionID: receipt.SessionID, StepID: receipt.StepID, BuilderTaskID: receipt.BuilderTaskID,
		SnapshotHash: runtimeAuthoringEncodeDigest(receipt.SnapshotHash), CoverageHash: runtimeAuthoringEncodeDigest(receipt.CoverageHash),
		ScenarioID: receipt.ScenarioID, Categories: runtimeAuthoringCategoryMask(receipt.Categories), Label: receipt.Label,
		Observation: receipt.Observation, Method: receipt.Method, Path: receipt.Path,
		Expected: receipt.ExpectedStatus, Actual: receipt.ActualStatus,
		RequestHash: runtimeAuthoringEncodeDigest(receipt.RequestHash), ResponseHash: runtimeAuthoringEncodeDigest(receipt.ResponseHash),
		IdempotencyKey: receipt.IdempotencyKey, IdempotencyReplayed: receipt.IdempotencyReplayed,
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return changeplanmodel.RuntimeAuthoringStepReceiptVersion + "." + encoded + "." + base64.RawURLEncoding.EncodeToString(s.sign(encoded)), nil
}

func (s *RuntimeAuthoringScenarioReceiptService) Verify(token, builderTaskID string, binding changeplanmodel.RuntimeAuthoringEvidenceBinding) (RuntimeAuthoringScenarioStepReceipt, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[0] != changeplanmodel.RuntimeAuthoringStepReceiptVersion {
		return RuntimeAuthoringScenarioStepReceipt{}, errors.New("runtime authoring scenario receipt format is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || s == nil || len(s.key) < sha256.Size || !hmac.Equal(signature, s.sign(parts[1])) {
		return RuntimeAuthoringScenarioStepReceipt{}, errors.New("runtime authoring scenario receipt signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return RuntimeAuthoringScenarioStepReceipt{}, errors.New("runtime authoring scenario receipt payload is invalid")
	}
	var wire runtimeAuthoringScenarioReceiptPayload
	if err := json.Unmarshal(payload, &wire); err != nil {
		return RuntimeAuthoringScenarioStepReceipt{}, errors.New("runtime authoring scenario receipt payload is invalid")
	}
	receipt := RuntimeAuthoringScenarioStepReceipt{
		Version:   changeplanmodel.RuntimeAuthoringStepReceiptVersion,
		SessionID: wire.SessionID, StepID: wire.StepID, BuilderTaskID: wire.BuilderTaskID,
		SnapshotHash: runtimeAuthoringDecodeDigest(wire.SnapshotHash), CoverageHash: runtimeAuthoringDecodeDigest(wire.CoverageHash),
		ScenarioID: wire.ScenarioID, Categories: runtimeAuthoringCategoriesFromMask(wire.Categories), Label: wire.Label,
		Observation: wire.Observation, Method: wire.Method, Path: wire.Path,
		ExpectedStatus: wire.Expected, ActualStatus: wire.Actual,
		RequestHash: runtimeAuthoringDecodeDigest(wire.RequestHash), ResponseHash: runtimeAuthoringDecodeDigest(wire.ResponseHash),
		IdempotencyKey: wire.IdempotencyKey, IdempotencyReplayed: wire.IdempotencyReplayed,
	}
	if err := validateRuntimeAuthoringScenarioReceipt(s, receipt); err != nil {
		return RuntimeAuthoringScenarioStepReceipt{}, err
	}
	if receipt.BuilderTaskID != strings.TrimSpace(builderTaskID) || receipt.SnapshotHash != strings.TrimSpace(binding.SnapshotHash) || receipt.CoverageHash != strings.TrimSpace(binding.CoverageHash) {
		return RuntimeAuthoringScenarioStepReceipt{}, errors.New("runtime authoring scenario receipt binding is stale")
	}
	return receipt, nil
}

func (s *RuntimeAuthoringScenarioReceiptService) sign(payload string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func normalizeRuntimeAuthoringScenarioReceipt(receipt RuntimeAuthoringScenarioStepReceipt) RuntimeAuthoringScenarioStepReceipt {
	receipt.SessionID = strings.TrimSpace(receipt.SessionID)
	receipt.StepID = strings.TrimSpace(receipt.StepID)
	receipt.BuilderTaskID = strings.TrimSpace(receipt.BuilderTaskID)
	receipt.SnapshotHash = strings.ToLower(strings.TrimSpace(receipt.SnapshotHash))
	receipt.CoverageHash = strings.ToLower(strings.TrimSpace(receipt.CoverageHash))
	receipt.ScenarioID = strings.TrimSpace(receipt.ScenarioID)
	receipt.Label = strings.TrimSpace(receipt.Label)
	receipt.Observation = strings.TrimSpace(receipt.Observation)
	receipt.Method = strings.ToUpper(strings.TrimSpace(receipt.Method))
	receipt.Path = strings.TrimSpace(receipt.Path)
	receipt.RequestHash = strings.ToLower(strings.TrimSpace(receipt.RequestHash))
	receipt.ResponseHash = strings.ToLower(strings.TrimSpace(receipt.ResponseHash))
	receipt.IdempotencyKey = strings.TrimSpace(receipt.IdempotencyKey)
	receipt.Categories = normalizedRuntimeAuthoringScenarioCategories(receipt.Categories)
	receipt.ExpectedStatus = normalizedRuntimeAuthoringExpectedStatuses(receipt.ExpectedStatus)
	return receipt
}

func validateRuntimeAuthoringScenarioReceipt(service *RuntimeAuthoringScenarioReceiptService, receipt RuntimeAuthoringScenarioStepReceipt) error {
	if err := validateRuntimeAuthoringScenarioReceiptIdentity(service, receipt); err != nil {
		return err
	}
	if !runtimeAuthoringReceiptSHA256(receipt.ResponseHash) || receipt.ActualStatus < 100 || receipt.ActualStatus > 599 {
		return errors.New("runtime authoring scenario receipt outcome is incomplete")
	}
	return nil
}

func validateRuntimeAuthoringScenarioReceiptIdentity(service *RuntimeAuthoringScenarioReceiptService, receipt RuntimeAuthoringScenarioStepReceipt) error {
	if service == nil || len(service.key) < sha256.Size {
		return errors.New("runtime authoring scenario receipt key is unavailable")
	}
	if receipt.Version != changeplanmodel.RuntimeAuthoringStepReceiptVersion || receipt.BuilderTaskID == "" || receipt.ScenarioID == "" || receipt.Label == "" || receipt.Method == "" || receipt.Path == "" {
		return errors.New("runtime authoring scenario receipt identity is incomplete")
	}
	if (receipt.SessionID == "") != (receipt.StepID == "") {
		return errors.New("runtime authoring scenario receipt evidence session identity is incomplete")
	}
	if !runtimeAuthoringReceiptSHA256(receipt.SnapshotHash) || !runtimeAuthoringReceiptSHA256(receipt.CoverageHash) || !runtimeAuthoringReceiptSHA256(receipt.RequestHash) {
		return errors.New("runtime authoring scenario receipt hash is invalid")
	}
	if len(receipt.Categories) == 0 || len(receipt.ExpectedStatus) == 0 {
		return errors.New("runtime authoring scenario receipt expectation is incomplete")
	}
	for _, category := range receipt.Categories {
		if !runtimeAuthoringScenarioCategoryAllowed(category) {
			return errors.New("runtime authoring scenario receipt category is invalid")
		}
	}
	if receipt.Observation != "" && receipt.Observation != "before_state" && receipt.Observation != "after_state" {
		return errors.New("runtime authoring scenario receipt observation is invalid")
	}
	for _, status := range receipt.ExpectedStatus {
		if status < 100 || status > 599 {
			return errors.New("runtime authoring scenario receipt expected status is invalid")
		}
	}
	return nil
}

func normalizedRuntimeAuthoringScenarioCategories(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizedRuntimeAuthoringExpectedStatuses(values []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}

func runtimeAuthoringScenarioCategoryAllowed(value string) bool {
	for _, category := range changeplanmodel.RuntimeAuthoringRequiredScenarioCategories {
		if value == category {
			return true
		}
	}
	return false
}

func runtimeAuthoringCategoryMask(values []string) uint16 {
	var mask uint16
	for _, value := range values {
		for index, category := range changeplanmodel.RuntimeAuthoringRequiredScenarioCategories {
			if value == category {
				mask |= 1 << index
				break
			}
		}
	}
	return mask
}

func runtimeAuthoringCategoriesFromMask(mask uint16) []string {
	if mask == 0 || mask>>len(changeplanmodel.RuntimeAuthoringRequiredScenarioCategories) != 0 {
		return nil
	}
	values := []string{}
	for index, category := range changeplanmodel.RuntimeAuthoringRequiredScenarioCategories {
		if mask&(1<<index) != 0 {
			values = append(values, category)
		}
	}
	return normalizedRuntimeAuthoringScenarioCategories(values)
}

func runtimeAuthoringEncodeDigest(value string) string {
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func runtimeAuthoringDecodeDigest(value string) string {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return ""
	}
	return hex.EncodeToString(raw)
}

func runtimeAuthoringReceiptSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
