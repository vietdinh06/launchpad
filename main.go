package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/moby/moby/client"

	"launchpad/internal/platform"
)

func printUsage() {
	fmt.Println("Launchpad - Local Deployment Platform")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  launchpad deploy <directory> <host-port> <app-port>")
	fmt.Println("  launchpad list")
	fmt.Println("  launchpad status <deployment-id>")
	fmt.Println("  launchpad logs <deployment-id>")
	fmt.Println("  launchpad stop <deployment-id>")
}

func run() error {
	if len(os.Args) < 2 {
		printUsage()
		return fmt.Errorf("missing command")
	}

	command := strings.ToLower(os.Args[1])

	switch command {
	case "deploy":
		if len(os.Args) != 5 {
			return fmt.Errorf("deploy requires a directory and two ports")
		}

	case "list":
		if len(os.Args) != 2 {
			return fmt.Errorf("list takes no arguments")
		}

	case "status", "logs", "stop":
		if len(os.Args) != 3 {
			return fmt.Errorf("%s requires a deployment ID", command)
		}

	default:
		printUsage()
		return fmt.Errorf("unknown command: %s", command)
	}

	ctx := context.Background()

	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("create Docker client: %w", err)
	}
	defer dockerClient.Close()

	switch command {
	case "deploy":
		return platform.DeployApplication(
			ctx,
			dockerClient,
			os.Args[2],
			os.Args[3],
			os.Args[4],
		)

	case "list":
		return platform.ListDeployments(ctx, dockerClient)

	case "status":
		return platform.DeploymentStatus(
			ctx, dockerClient, os.Args[2],
		)

	case "logs":
		return platform.DeploymentLogs(
			ctx, dockerClient, os.Args[2],
		)

	case "stop":
		return platform.StopDeployment(
			ctx, dockerClient, os.Args[2],
		)
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Println("Error:", err)
		os.Exit(1)
	}
}
