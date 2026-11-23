package deployment

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"launchpad/internal/runtime"
)

type fakeRuntime struct {
	deployErr  error
	inspectErr error
	running    bool
}

var _ runtime.Runtime = (*fakeRuntime)(nil)

func (f *fakeRuntime) Deploy(
	ctx context.Context,
	req runtime.DeployRequest,
) (runtime.Instance, error) {

	if f.deployErr != nil {
		return runtime.Instance{}, f.deployErr
	}

	return runtime.Instance{
		ID: "fake-container-123",
	}, nil
}

func (f *fakeRuntime) Inspect(
	ctx context.Context,
	id string,
) (runtime.InstanceStatus, error) {

	if f.inspectErr != nil {
		return runtime.InstanceStatus{}, f.inspectErr
	}

	status := "exited"
	exitCode := 1

	if f.running {
		status = "running"
		exitCode = 0
	}

	return runtime.InstanceStatus{
		ID:       id,
		Running:  f.running,
		Status:   status,
		ExitCode: exitCode,
	}, nil
}

func (f *fakeRuntime) Stop(
	ctx context.Context,
	id string,
) error {
	return nil
}

func (f *fakeRuntime) Remove(
	ctx context.Context,
	id string,
) error {
	return nil
}

func (f *fakeRuntime) Logs(
	ctx context.Context,
	id string,
) (io.ReadCloser, error) {

	return io.NopCloser(
		strings.NewReader("fake application log"),
	), nil
}

func TestExecuteSuccess(t *testing.T) {
	fake := &fakeRuntime{
		running: true,
	}

	executor := NewExecutor(fake)

	instance, err := executor.Execute(
		context.Background(),
		runtime.DeployRequest{},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if instance.ID != "fake-container-123" {
		t.Errorf("unexpected ID: %s", instance.ID)
	}
}

func TestExecuteDeploymentFailure(t *testing.T) {
	expectedErr := errors.New("image not found")

	fake := &fakeRuntime{
		deployErr: expectedErr,
	}

	executor := NewExecutor(fake)

	_, err := executor.Execute(
		context.Background(),
		runtime.DeployRequest{},
	)

	if !errors.Is(err, expectedErr) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteContainerCrash(t *testing.T) {
	fake := &fakeRuntime{
		running: false,
	}

	executor := NewExecutor(fake)

	instance, err := executor.Execute(
		context.Background(),
		runtime.DeployRequest{},
	)

	if err == nil {
		t.Fatal("expected startup failure")
	}

	if instance.ID == "" {
		t.Fatal("expected failed instance ID")
	}
}

func TestExecuteCancellation(t *testing.T) {
	fake := &fakeRuntime{
		running: true,
	}

	executor := NewExecutor(fake)

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	_, err := executor.Execute(
		ctx,
		runtime.DeployRequest{},
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
