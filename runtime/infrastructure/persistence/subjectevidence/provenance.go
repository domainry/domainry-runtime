package subjectevidence

import (
	"context"
	"encoding/json"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type SubjectProvenance struct {
	Resources             []recordmodel.SubjectRecordReference
	EventIDs              []string
	PublicationMessageIDs []string
}

func provenance(p plan) SubjectProvenance {
	out := SubjectProvenance{Resources: p.Resources, EventIDs: p.EventIDs, PublicationMessageIDs: []string{}}
	for _, row := range p.Rows {
		if row.Table == "_publication_outbox" {
			out.PublicationMessageIDs = append(out.PublicationMessageIDs, row.ID)
		}
	}
	return out
}
func (h *Handler) SubjectProvenance(ctx context.Context, workspace, subject string) (SubjectProvenance, error) {
	p, err := h.inventory(ctx, workspace, subject)
	return provenance(p), err
}
func (h *Handler) PreparedSubjectProvenance(ctx context.Context, request, workspace, subject string) (SubjectProvenance, error) {
	raw, err := h.PrepareSubjectErasure(ctx, request, workspace, subject)
	if err != nil {
		return SubjectProvenance{}, err
	}
	var p plan
	err = json.Unmarshal(raw, &p)
	return provenance(p), err
}
