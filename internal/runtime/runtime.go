package runtime

import (
	"context"
	"io"
)

type DeployRequest struct {
	JobID         string
	ApplicationID string
	ReleaseID     string
	ImageRef      string
	ContainerPort int
	HostPort      int

	NetworkName   string
	Hostname      string
}

type Instance struct {
	ID string
}

type InstanceStatus struct {
	ID       string
	Running  bool
	Status   string
	ExitCode int
}

type Runtime interface {
	Deploy(
		ctx context.Context,
		req DeployRequest,
	) (Instance, error)

	Inspect(
		ctx context.Context,
		instanceID string,
	) (InstanceStatus, error)

	Stop(
		ctx context.Context,
		instanceID string,
	) error

	Remove(
		ctx context.Context,
		instanceID string,
	) error

	Logs(
		ctx context.Context,
		instanceID string,
	) (io.ReadCloser, error)
}
