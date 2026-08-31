package scheduler

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-scheduler-sdk/schedule"
)

func validateSchedulerDefinitionContract(ctx context.Context, data map[string]any) error {
	if err := schedule.ValidateDefinitionData(ctx, data); err != nil {
		return apperror.FromError(apperror.KindBadRequest, err)
	}
	return nil
}

func validateSchedulerScheduleFragment(ctx context.Context, data map[string]any) error {
	if err := schedule.ValidateData(ctx, data); err != nil {
		return apperror.FromError(apperror.KindBadRequest, err)
	}
	return nil
}
