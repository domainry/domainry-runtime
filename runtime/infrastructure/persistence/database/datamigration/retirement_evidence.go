package datamigration

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

type RetirementBusinessCheck struct {
	Name             string `json:"name"`
	SourceValue      string `json:"source_value"`
	ReplacementValue string `json:"replacement_value"`
	EvidenceRef      string `json:"evidence_ref"`
}

type RetirementEvidenceInput struct {
	Owner               string                    `json:"owner"`
	Replacement         string                    `json:"replacement"`
	Disposition         string                    `json:"disposition"`
	DispositionApproval string                    `json:"disposition_approval,omitempty"`
	RecordedAt          time.Time                 `json:"recorded_at"`
	BusinessChecks      []RetirementBusinessCheck `json:"business_checks"`
}

type RetirementEvidenceReport struct {
	GeneratedAt          time.Time                 `json:"generated_at"`
	Owner                string                    `json:"owner"`
	Replacement          string                    `json:"replacement"`
	Disposition          string                    `json:"disposition"`
	DispositionApproval  string                    `json:"disposition_approval,omitempty"`
	BusinessChecks       []RetirementBusinessCheck `json:"business_checks"`
	Verification         VerificationReport        `json:"verification"`
	OrphanRows           int64                     `json:"orphan_rows"`
	DuplicateRows        int64                     `json:"duplicate_rows"`
	InvalidWorkspaceRows int64                     `json:"invalid_workspace_rows"`
	InvalidReferenceRows int64                     `json:"invalid_reference_rows"`
	Ready                bool                      `json:"ready"`
	Blockers             []string                  `json:"blockers,omitempty"`
}

func LoadRetirementEvidenceInput(path string) (RetirementEvidenceInput, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return RetirementEvidenceInput{}, fmt.Errorf("retirement evidence requires --business-evidence")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return RetirementEvidenceInput{}, fmt.Errorf("read retirement business evidence: %w", err)
	}
	var input RetirementEvidenceInput
	if err := timevalue.UnmarshalJSON(raw, &input); err != nil {
		return RetirementEvidenceInput{}, fmt.Errorf("parse retirement business evidence: %w", err)
	}
	return input, nil
}

func BuildRetirementEvidence(verification VerificationReport, input RetirementEvidenceInput, now time.Time) RetirementEvidenceReport {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := RetirementEvidenceReport{
		GeneratedAt: now.UTC(), Owner: strings.TrimSpace(input.Owner), Replacement: strings.TrimSpace(input.Replacement),
		Disposition: strings.TrimSpace(input.Disposition), DispositionApproval: strings.TrimSpace(input.DispositionApproval),
		BusinessChecks: append([]RetirementBusinessCheck(nil), input.BusinessChecks...), Verification: verification,
	}
	if report.Owner == "" || report.Replacement == "" {
		report.Blockers = append(report.Blockers, "owner and replacement are required")
	}
	allowedDisposition := map[string]bool{"migrate": true, "archive": true, "anonymize": true, "retain": true, "discard": true}
	if !allowedDisposition[report.Disposition] {
		report.Blockers = append(report.Blockers, "owner disposition must be migrate, archive, anonymize, retain, or discard")
	}
	if (report.Disposition == "anonymize" || report.Disposition == "discard") && report.DispositionApproval == "" {
		report.Blockers = append(report.Blockers, "anonymize or discard requires disposition approval")
	}
	if input.RecordedAt.IsZero() || input.RecordedAt.After(now.Add(5*time.Minute)) {
		report.Blockers = append(report.Blockers, "owner evidence recorded_at is missing or in the future")
	}
	if len(report.BusinessChecks) == 0 {
		report.Blockers = append(report.Blockers, "at least one owner-defined business aggregate check is required")
	}
	for index := range report.BusinessChecks {
		check := &report.BusinessChecks[index]
		check.Name, check.SourceValue, check.ReplacementValue, check.EvidenceRef = strings.TrimSpace(check.Name), strings.TrimSpace(check.SourceValue), strings.TrimSpace(check.ReplacementValue), strings.TrimSpace(check.EvidenceRef)
		if check.Name == "" || check.SourceValue == "" || check.ReplacementValue == "" || check.EvidenceRef == "" {
			report.Blockers = append(report.Blockers, fmt.Sprintf("business check %d is incomplete", index))
		} else if check.SourceValue != check.ReplacementValue {
			report.Blockers = append(report.Blockers, "business check "+check.Name+" differs")
		}
	}
	if !verification.Current {
		report.Blockers = append(report.Blockers, "row/key/hash/sample or data-integrity verification is not current")
	}
	for _, table := range verification.Tables {
		report.DuplicateRows += table.SourceDuplicateRows + table.TargetDuplicateRows
		report.InvalidWorkspaceRows += table.SourceInvalidWorkspaces + table.TargetInvalidWorkspaces
		report.InvalidReferenceRows += table.SourceInvalidReferences + table.TargetInvalidReferences
	}
	report.OrphanRows = report.InvalidReferenceRows
	report.Ready = len(report.Blockers) == 0
	return report
}
