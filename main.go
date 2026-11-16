package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"log"
	"net/netip"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/moby/moby/api/pkg/stdcopy"
)

func main() {
	fmt.Println("Starting Launchpad...")

	// Create a context for our Docker operations.
	ctx := context.Background()

	// Create a client to communicate with Docker Engine.
	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		log.Fatal("Failed to create Docker client: ", err)
	}
	defer dockerClient.Close()

	// Ask Docker for its running containers.
	result, err := dockerClient.ContainerList(
		ctx,
		client.ContainerListOptions{All: true},
	)
	if err != nil {
		log.Fatal("Failed to list containers: ", err)
	}

	fmt.Println("Connected to Docker!")
	fmt.Println("Running containers:")

	for _, container := range result.Items {
		fmt.Printf("ID: %s | Image: %s\n",
			container.ID[:12],
			container.Image,
		)
	}

	imageName := "docker.io/library/nginx:latest"

	reader, err := dockerClient.ImagePull(
		ctx,
		imageName,
		client.ImagePullOptions{},
	)
	if err != nil {
		log.Fatal("Failed to pull image: ", err)
	}
	defer reader.Close()

	// Consume the response stream to finish the pull.
	if _, err := io.Copy(io.Discard, reader); err != nil {
		log.Fatal("Failed to complete image pull: ", err)
	}

	containerName := "launchpad-nginx"
	if containerID := findContainerID(result.Items, containerName); containerID != "" {
		_, err := dockerClient.ContainerRemove(
			ctx,
			containerID,
			client.ContainerRemoveOptions{
				Force: true,
			},
		)
		if err != nil {
			log.Fatal("Failed to remove existing container: ", err)
		}
	}

	containerPort := network.MustParsePort("80/tcp")

	resp, err := dockerClient.ContainerCreate(
		ctx,
		client.ContainerCreateOptions{
			Name: "launchpad-nginx",

			Config: &container.Config{
				Image: imageName,
				ExposedPorts: network.PortSet{
					containerPort: struct{}{},
				},
			},

			HostConfig: &container.HostConfig{
				PortBindings: network.PortMap{
					containerPort: []network.PortBinding{
						{
							HostIP:   netip.MustParseAddr("127.0.0.1"),
							HostPort: "8080",
						},
					},
				},
			},
		},
	)
	if err != nil {
		log.Fatal("Failed to create container: ", err)
	}

	_, err = dockerClient.ContainerStart(
		ctx,
		resp.ID,
		client.ContainerStartOptions{},
	)

	if err != nil {
		log.Fatal("Failed to start container: ", err)
	}
	fmt.Printf("Container started: %s\n", resp.ID)

	inspect, err := dockerClient.ContainerInspect(
		ctx,
		resp.ID,
		client.ContainerInspectOptions{},
	)

	if err != nil {
		log.Fatal("Failed to inspect container: ", err)
	}
	fmt.Printf("Container inspect: %v\n", inspect)

	fmt.Println("Container Information:")
	fmt.Println("ID:", inspect.Container.ID )
	fmt.Println("Name:", inspect.Container.Name)
	fmt.Println("Running:", inspect.Container.State.Running)
	fmt.Println("Status:", inspect.Container.State.Status)

	logs, err := dockerClient.ContainerLogs(
		ctx,
		resp.ID,
		client.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Tail: "15",
		},
	)

	if err != nil {
		log.Fatal("Failed to retrieve logs", err)
	}
	defer logs.Close()

	fmt.Println("Container logs:")

	_, err = stdcopy.StdCopy(
		os.Stdout,
		os.Stderr,
		logs,
	)

	if err != nil {
		log.Fatal("Failed to retrieve logs", err)
	}

}

func findContainerID(containers []container.Summary, name string) string {
	for _, container := range containers {
		for _, containerName := range container.Names {
			if containerName == "/"+name || containerName == name {
				return container.ID
			}
		}
	}
	return ""
}
