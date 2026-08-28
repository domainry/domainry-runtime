package integration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	"go.uber.org/zap"
)

const defaultInvocationReconciliationAge = 5 * time.Minute

type InvocationReconciliationResult struct {
	Examined         int                                         `json:"examined"`
	Marked           int                                         `json:"marked"`
	Skipped          int                                         `json:"skipped"`
	Facts            []integrationmodel.IntegrationInvocation    `json:"facts"`
	Acknowledgements []integrationmodel.IntegrationOutboxMessage `json:"acknowledgements"`
}

func (s *IntegrationApplicationService) StartInvocationReconciliationWorker(ctx context.Context, interval, staleAfter time.Duration, limit int, scope principalmodel.SystemScope) <-chan struct{} {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return workerplatform.Stopped()
	}
	_, invocationOK := s.deliveryRepo.(integrationrepository.IntegrationInvocationReconciliationRepository)
	_, acknowledgementOK := s.deliveryRepo.(integrationrepository.IntegrationAcknowledgementReconciliationRepository)
	if !invocationOK && !acknowledgementOK {
		return workerplatform.Stopped()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if staleAfter <= 0 {
		staleAfter = defaultInvocationReconciliationAge
	}
	if limit <= 0 {
		limit = 100
	}
	return workerplatform.StartNamedLoop(ctx, "integration_invocation_reconciliation", interval, func() {
		s.worker.Control.RunIfAccepting(func() {
			s.processInvocationReconciliationTick(ctx, staleAfter, limit, scope)
		})
	})
}

func (s *IntegrationApplicationService) processInvocationReconciliationTick(ctx context.Context, staleAfter time.Duration, limit int, scope principalmodel.SystemScope) {
	result, err := s.ReconcileMissingIntegrationReceipts(ctx, staleAfter, limit, scope)
	if err != nil {
		if ctx.Err() == nil {
			logging.FromContext(ctx).Error("integration invocation reconciliation failed", zap.String("error_code", stableIntegrationFailureCode(err, "backend.integration.invocation.reconciliation_failed")))
		}
		return
	}
	if result.Marked > 0 {
		logging.FromContext(ctx).Warn("integration invocations require reconciliation", zap.Int("marked", result.Marked), zap.Int("examined", result.Examined))
	}
}

func (s *IntegrationApplicationService) ReconcileMissingIntegrationReceipts(ctx context.Context, staleAfter time.Duration, limit int, scope principalmodel.SystemScope) (InvocationReconciliationResult, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return InvocationReconciliationResult{}, fmt.Errorf("backend.system_scope_required: %w", err)
	}
	invocationRepository, invocationOK := s.deliveryRepo.(integrationrepository.IntegrationInvocationReconciliationRepository)
	acknowledgementRepository, acknowledgementOK := s.deliveryRepo.(integrationrepository.IntegrationAcknowledgementReconciliationRepository)
	if !invocationOK && !acknowledgementOK {
		return InvocationReconciliationResult{}, fmt.Errorf("backend.integration.invocation.reconciliation_repository_unavailable")
	}
	if staleAfter <= 0 {
		staleAfter = defaultInvocationReconciliationAge
	}
	if limit <= 0 {
		limit = 100
	} else if limit > 500 {
		limit = 500
	}
	now := s.worker.Clock.Now()
	result := InvocationReconciliationResult{Facts: []integrationmodel.IntegrationInvocation{}, Acknowledgements: []integrationmodel.IntegrationOutboxMessage{}}
	if invocationOK {
		due, err := invocationRepository.ListPreparedInvocationsForReconciliation(ctx, scope, limit, now.Add(-staleAfter).Format(time.RFC3339))
		if err != nil {
			return InvocationReconciliationResult{}, err
		}
		if s.operationalMetrics != nil {
			s.operationalMetrics.observeReconciliationLag(due, now)
		}
		result.Examined += len(due)
		for _, fact := range due {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			marked, changed, err := invocationRepository.MarkInvocationReconciliationRequired(ctx, fact.WorkspaceID, fact.ID, now.Format(time.RFC3339))
			if err != nil {
				return result, err
			}
			if !changed {
				result.Skipped++
				continue
			}
			result.Marked++
			result.Facts = append(result.Facts, marked)
			principal := integrationruntime.IntegrationWorkerPrincipal(marked.WorkspaceID)
			s.audit(ctx, "integration_invocation_reconciliation_required", "integration_invocation", marked.ID, principal,
				"Integration invocation requires external receipt reconciliation", integrationprojection.IntegrationInvocationAuditShape(fact), integrationprojection.IntegrationInvocationAuditShape(marked), map[string]any{
					"workspace_id": marked.WorkspaceID, "connector_key": marked.ConnectorKey, "connection_key": marked.ConnectionKey,
					"operation": marked.Operation, "request_ref": marked.RequestRef, "business_fact_exists": true,
					"external_receipt_missing": strings.TrimSpace(marked.ResponseRef) == "",
				})
		}
	}
	if acknowledgementOK {
		overdue, err := acknowledgementRepository.ListOverdueOutboxAcknowledgements(ctx, scope, limit, now.Format(time.RFC3339))
		if err != nil {
			return result, err
		}
		result.Examined += len(overdue)
		for _, message := range overdue {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			marked, changed, err := acknowledgementRepository.MarkOutboxAcknowledgementReconciliationRequired(ctx, message.WorkspaceID, message.ID, now.Format(time.RFC3339))
			if err != nil {
				return result, err
			}
			if !changed {
				result.Skipped++
				continue
			}
			result.Marked++
			result.Acknowledgements = append(result.Acknowledgements, marked)
			principal := integrationruntime.IntegrationWorkerPrincipal(marked.WorkspaceID)
			s.audit(ctx, "integration_outbox_ack_reconciliation_required", "integration_outbox", marked.ID, principal,
				"Integration outbox acknowledgement requires reconciliation", integrationprojection.IntegrationOutboxAuditShape(message), integrationprojection.IntegrationOutboxAuditShape(marked), map[string]any{
					"workspace_id": marked.WorkspaceID, "connector_key": marked.ConnectorKey, "connection_key": marked.ConnectionKey,
					"operation": marked.Operation, "response_ref": marked.ResponseRef, "ack_timeout": true,
				})
		}
	}
	return result, nil
}
