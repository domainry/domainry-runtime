package record

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
)

// CandidateScopeMatches evaluates create authorization in the current Action
// transaction, so related records written earlier in the same unit of work are
// visible without inserting the candidate root record first.
func (r RecordStore) CandidateScopeMatches(ctx context.Context, workspaceID string, candidate recordmodel.Record, expression recordmodel.RecordScopeExpression) (bool, error) {
	return r.candidateScopeMatches(ctx, workspaceID, candidate, expression, recordservice.RecordPlannedRelations(ctx))
}

func (r RecordStore) candidateScopeMatches(
	ctx context.Context,
	workspaceID string,
	candidate recordmodel.Record,
	expression recordmodel.RecordScopeExpression,
	planned map[string]map[string]recordmodel.Record,
) (bool, error) {
	switch expression.Operator {
	case "and":
		if len(expression.Children) < 2 {
			return false, fmt.Errorf("candidate scope and requires at least two children")
		}
		for _, child := range expression.Children {
			matched, err := r.candidateScopeMatches(ctx, workspaceID, candidate, child, planned)
			if err != nil || !matched {
				return matched, err
			}
		}
		return true, nil
	case "or":
		if len(expression.Children) < 2 {
			return false, fmt.Errorf("candidate scope or requires at least two children")
		}
		for _, child := range expression.Children {
			matched, err := r.candidateScopeMatches(ctx, workspaceID, candidate, child, planned)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	case "not":
		if len(expression.Children) != 1 {
			return false, fmt.Errorf("candidate scope not requires exactly one child")
		}
		matched, err := r.candidateScopeMatches(ctx, workspaceID, candidate, expression.Children[0], planned)
		return !matched, err
	}
	if len(expression.Path) > 0 {
		first := expression.Path[0]
		related := plannedRelationCandidates(candidate, first, planned)
		if len(related) > 0 {
			remaining := expression
			remaining.Path = append([]recordmodel.RecordScopePathSegment(nil), expression.Path[1:]...)
			for _, record := range related {
				matched, err := r.candidateScopeMatches(ctx, workspaceID, record, remaining, planned)
				if err != nil {
					return false, err
				}
				if matched {
					return true, nil
				}
			}
			return false, nil
		}
	}
	executor := r.queryExecutor(ctx)
	return querypersistence.CandidateScopeMatches(r.store, workspaceID, candidate, expression, func(statement string, args ...any) (bool, error) {
		var marker string
		err := executor.QueryRowContext(ctx, statement, args...).Scan(&marker)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	})
}

func plannedRelationCandidates(
	candidate recordmodel.Record,
	segment recordmodel.RecordScopePathSegment,
	planned map[string]map[string]recordmodel.Record,
) []recordmodel.Record {
	targets := planned[strings.TrimSpace(segment.TargetObjectKey)]
	if len(targets) == 0 {
		return nil
	}
	switch strings.TrimSpace(segment.Direction) {
	case "forward":
		recordID := strings.TrimSpace(fmt.Sprint(candidate.Data[strings.TrimSpace(segment.RelationFieldKey)]))
		if record, found := targets[recordID]; found {
			return []recordmodel.Record{record}
		}
	case "reverse":
		result := []recordmodel.Record{}
		for _, record := range targets {
			if strings.TrimSpace(fmt.Sprint(record.Data[strings.TrimSpace(segment.RelationFieldKey)])) == strings.TrimSpace(candidate.ID) {
				result = append(result, record)
			}
		}
		return result
	}
	return nil
}
