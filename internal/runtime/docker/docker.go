package docker

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"launchpad/internal/runtime"
)

type DockerRuntime struct {
	client *client.Client
}

var _ runtime.Runtime = (*DockerRuntime)(nil)

func New(c *client.Client) *DockerRuntime {
	return &DockerRuntime{
		client: c,
	}
}

func (d *DockerRuntime) Deploy(ctx context.Context, req runtime.DeployRequest) (runtime.Instance, error) {
	if strings.TrimSpace(req.ImageRef) == "" {
		return runtime.Instance{}, fmt.Errorf("Image reference is required.")
	}

	if req.JobID == "" {
		return runtime.Instance{}, fmt.Errorf("Job ID is required.")
	}

	if req.HostPort < 0 || req.HostPort > 65535 {
		return runtime.Instance{}, fmt.Errorf("Host port not valid.")
	}

	if req.ContainerPort < 1 || req.ContainerPort > 65535 {
		return runtime.Instance{}, fmt.Errorf("Container port not valid.")
	}

	port := network.MustParsePort(
		strconv.Itoa(req.ContainerPort) + "/tcp",
	)

	hostPort := ""
	if req.HostPort != 0 {
		hostPort = strconv.Itoa(req.HostPort)
	}

	resp, err := d.client.ContainerCreate(
		ctx,
		client.ContainerCreateOptions{
			Name: "launchpad-" + req.JobID,

			Config: &container.Config{
				Image: req.ImageRef,

				ExposedPorts: network.PortSet{
					port: struct{}{},
				},

				Labels: map[string]string{
					"launchpad.managed":        "true",
					"launchpad.job_id":         req.JobID,
					"launchpad.application_id": req.ApplicationID,
					"launchpad.release_id":     req.ReleaseID,
				},
			},

			HostConfig: &container.HostConfig{
				PortBindings: network.PortMap{
					port: []network.PortBinding{
						{
							HostIP: netip.MustParseAddr(
								"127.0.0.1",
							),
							HostPort: hostPort,
						},
					},
				},
			},
		},
	)

	if err != nil {
		return runtime.Instance{}, fmt.Errorf("create container: %w", err)
	}

	_, err = d.client.ContainerStart(
		ctx,
		resp.ID,
		client.ContainerStartOptions{},
	)

	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(), 10*time.Second,
		)
		defer cancel()

		_, cleanupErr := d.client.ContainerRemove(
			cleanupCtx,
			resp.ID,
			client.ContainerRemoveOptions{
				Force: true,
			},
		)

		if cleanupErr != nil {
			return runtime.Instance{ID: resp.ID},
				fmt.Errorf(
					"start container: %w; cleanup failed: %v",
					err, cleanupErr,
				)
		}

		return runtime.Instance{}, fmt.Errorf("start container: %w", err)
	}

	return runtime.Instance{
		ID: resp.ID,
	}, nil
}

func (d *DockerRuntime) Inspect(ctx context.Context, instanceID string) (runtime.InstanceStatus, error) {
	result, err := d.client.ContainerInspect(
		ctx,
		instanceID,
		client.ContainerInspectOptions{},
	)

	if err != nil {
		return runtime.InstanceStatus{}, fmt.Errorf("inspect container: %w", err)
	}

	state := result.Container.State

	if state == nil {
		return runtime.InstanceStatus{},
			fmt.Errorf("container has no state")
	}

	return runtime.InstanceStatus{
		ID:       result.Container.ID,
		Running:  state.Running,
		Status:   string(state.Status),
		ExitCode: state.ExitCode,
	}, nil
}

func (d *DockerRuntime) Stop(ctx context.Context, instanceID string) error {
	timeout := 10

	_, err := d.client.ContainerStop(
		ctx,
		instanceID,
		client.ContainerStopOptions{
			Timeout: &timeout,
		},
	)

	if err != nil {
		return fmt.Errorf("stop container: %w", err)
	}

	return nil
}

func (d *DockerRuntime) Remove(ctx context.Context, instanceID string) error {
	_, err := d.client.ContainerRemove(
		ctx,
		instanceID,
		client.ContainerRemoveOptions{},
	)

	if err != nil {
		return fmt.Errorf("remove container: %w", err)
	}

	return nil
}

func (d *DockerRuntime) Logs(ctx context.Context, instanceID string) (io.ReadCloser, error) {
	result, err := d.client.ContainerLogs(
		ctx,
		instanceID,
		client.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Tail:       "100",
		},
	)

	if err != nil {
		return nil, fmt.Errorf("container logs: %w", err)
	}

	reader, writer := io.Pipe()

	go func() {
		defer result.Close()

		_, err := stdcopy.StdCopy(
			writer,
			writer,
			result,
		)

		writer.CloseWithError(err)
	}()

	return reader, nil
}
