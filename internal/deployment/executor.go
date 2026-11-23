package deployment

import (
	"context"
	"fmt"
	"time"

	"launchpad/internal/runtime"
)

type Executor struct {
	runtime runtime.Runtime
}

func NewExecutor(r runtime.Runtime) *Executor {
	return &Executor{
		runtime: r,
	}
}

func (e *Executor) Execute(ctx context.Context, req runtime.DeployRequest) (runtime.Instance, error) {
	instance, err := e.runtime.Deploy(ctx, req)

	if err != nil {
		return instance,
			fmt.Errorf("deployment failed: %w", err)
	}

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return instance, ctx.Err()

	case <-timer.C:
	}

	status, err := e.runtime.Inspect(
		ctx,
		instance.ID,
	)

	if err != nil {
		return instance,
			fmt.Errorf("inspect deployment: %w", err)
	}

	if !status.Running {
		return instance, fmt.Errorf(
			"application failed to stay running: "+
				"status=%s, exit_code=%d",
			status.Status,
			status.ExitCode,
		)
	}

	return instance, nil
}
