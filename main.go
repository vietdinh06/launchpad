package main

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
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

	resp, err := dockerClient.ContainerCreate(
		ctx,
		client.ContainerCreateOptions{
			Image: imageName,
			Name:  containerName,
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
