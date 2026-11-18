
package main

import (
    "archive/tar"
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/netip"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"
	"github.com/moby/moby/api/pkg/stdcopy"
    "github.com/moby/moby/api/types/container"
    "github.com/moby/moby/api/types/network"
    "github.com/moby/moby/client"
)

const maxContextSize = 25 << 20 // 25 MiB

// createBuildContext packages the two files in our
// example application into an in-memory tar archive.
func createBuildContext(appPath string) (*bytes.Buffer, error) {
    var buffer bytes.Buffer
    tarWriter := tar.NewWriter(&buffer)
	var totalSize int64

	err := filepath.WalkDir(appPath,
		func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
                return walkErr
            }

			if entry.Name() == ".git" && entry.IsDir() {
				return filepath.SkipDir
			}

			if entry.Name() == ".env" {
                return nil
            }

			rel, err := filepath.Rel(appPath, path)

			if err != nil {
				return err
			}

			if rel == "." {
				return nil
			}

			info, err := entry.Info()

			if err != nil {
				return err
			}

			if !info.Mode().IsRegular() && !info.IsDir() {
                return fmt.Errorf("unsupported file: %s", rel)
            }

            if info.Mode().IsRegular() {
                totalSize += info.Size()
				if totalSize > maxContextSize {
					return fmt.Errorf("build context exceeds 25 MiB")
				}
            }

			header, err := tar.FileInfoHeader(info, "")

			if err != nil {
				return err
			}

			header.Name = filepath.ToSlash(rel)

			if err := tarWriter.WriteHeader(header); err != nil {
                return err
            }

            if info.IsDir() {
                return nil
            }

            file, err := os.Open(path)

			if err != nil {
				return err
			}

            _, copyErr := io.Copy(tarWriter, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr

		},
	)

	if err != nil {
		tarWriter.Close()
		return nil, err
	}

    if err := tarWriter.Close(); err != nil {
        return nil, err
    }

    return &buffer, nil
    
}

// readBuildOutput consumes Docker's build-event stream.
// It prints progress and returns any build error.
func readBuildOutput(reader io.Reader) error {
    decoder := json.NewDecoder(reader)

    for {
        var event struct {
            Stream string `json:"stream"`
            Error  string `json:"error"`
        }

        err := decoder.Decode(&event)
        if err == io.EOF {
            return nil
        }
        if err != nil {
            return err
        }

        if event.Error != "" {
            return fmt.Errorf("Docker build: %s", event.Error)
        }

        if event.Stream != "" {
            fmt.Print(event.Stream)
        }
    }
}

func validatePort(value string) error {
    port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
        return fmt.Errorf("invalid port: %s", value)
	}
	return nil
}

func deployApplication(
    ctx context.Context,
    dockerClient *client.Client,
    appPath string,
    hostPort string,
    appPort string,
) error {
 	fmt.Println("[1/5] Validating application...")

   	if err := validatePort(hostPort); err != nil {
        return err
    }
    if err := validatePort(appPort); err != nil {
        return err
    }

	absPath, err := filepath.Abs(appPath)
	if err != nil {
		return err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}

    if !info.IsDir() {
        return fmt.Errorf("application path is not a directory")
    }

	dockerfile := filepath.Join(absPath, "Dockerfile")
    if _, err := os.Stat(dockerfile); err != nil {
        return fmt.Errorf("Dockerfile not found: %w", err)
    }

	fmt.Println("[2/5] Preparing build context...")

    buildContext, err := createBuildContext(absPath)
    if err != nil {
        return fmt.Errorf("prepare context: %w", err)
    }

	 // A unique identifier avoids reusing an existing container name.
    deploymentID := strconv.FormatInt(time.Now().UnixNano(), 10)
    imageName := "launchpad-app:" + deploymentID
    containerName := "launchpad-" + deploymentID

	fmt.Println("[3/5] Building Docker image...")

	buildResult, err := dockerClient.ImageBuild(
		ctx,
		buildContext,
		client.ImageBuildOptions{
			Tags: []string{imageName},
		},
	)
    if err != nil {
    	return fmt.Errorf("build request failed: %w", err)
    }
    defer buildResult.Body.Close()

    if err := readBuildOutput(buildResult.Body); err != nil {
    	return fmt.Errorf("image build failed: %w", err)
    }

    // Check that the expected image was produced.
    if _, err := dockerClient.ImageInspect(ctx, imageName); err != nil {
        return fmt.Errorf("image verification failed: %w", err)
    }

    fmt.Println("[4/5] Creating container...")

    port := network.MustParsePort(appPort + "/tcp")

	created, err := dockerClient.ContainerCreate(
		ctx,
		client.ContainerCreateOptions{
			Name: containerName,

			Config: &container.Config{
                Image: imageName,
                ExposedPorts: network.PortSet{
                    port: struct{}{},
                },

				Labels: map[string]string{
					"launchpad.managed":       "true",
					"launchpad.deployment.id": deploymentID,
					"launchpad.application":   filepath.Base(absPath),
					"launchpad.host-port":     hostPort,
				},
            },

			HostConfig: &container.HostConfig{
				PortBindings: network.PortMap{
                    port: []network.PortBinding{
                        {
                            HostIP: netip.MustParseAddr("127.0.0.1"),
                            HostPort: hostPort,
                        },
                    },
                },
			},
		},
	)
    if err != nil {
        return fmt.Errorf("create container: %w", err)
    }

	fmt.Println("[5/5] Starting application...")

    _, err = dockerClient.ContainerStart(
        ctx,
        created.ID,
        client.ContainerStartOptions{},
    )

	if err != nil {
        return fmt.Errorf("start container: %w", err)
    }

    fmt.Println()
    fmt.Println("Deployment created!")
    fmt.Println("Deployment ID:", deploymentID)
    fmt.Println("Container:", containerName)
    fmt.Println("Image:", imageName)
    fmt.Println("Application URL: http://localhost:" + hostPort)
    fmt.Println("Note: HTTP readiness has not yet been verified.")

    return nil
}

func listDeployments(ctx context.Context, dockerClient *client.Client) error {
	filters := make(client.Filters).Add(
		"label",
		"launchpad.managed=true",
	)

	list, err := dockerClient.ContainerList(
		ctx,
		client.ContainerListOptions{
			All: true,
			Filters: filters,
		},
	)
	if err != nil {
        return fmt.Errorf("list deployments: %w", err)
	}

	if len(list.Items) == 0 {
		fmt.Println("No Launchpad deployments found.")
        return nil
	}

	fmt.Printf(
        "%-22s %-20s %-12s %-8s\n",
        "DEPLOYMENT",
        "APPLICATION",
        "STATE",
        "PORT",
    )

	for _, c := range list.Items {
        fmt.Printf(
            "%-22s %-20s %-12s %-8s\n",
            c.Labels["launchpad.deployment.id"],
            c.Labels["launchpad.application"],
            c.State,
            c.Labels["launchpad.host-port"],
        )
    }

    return nil
}

func findDeployment(ctx context.Context, dockerClient *client.Client, deploymentID string) (string, error){
	filters := make(client.Filters).
		Add("label", "launchpad.managed=true").
		Add("label", "launchpad.deployment,id="+deploymentID)

	list, err := dockerClient.ContainerList(
		ctx,
		client.ContainerListOptions{
			All: true,
			Filters: filters,
		},
	)
    if err != nil {
        return "", fmt.Errorf("find deployment: %w", err)
    }

	for _, c := range list.Items {
			if c.Labels["launchpad.deployment.id"] == deploymentID {
				return c.ID, nil
			}
		}

		return "", fmt.Errorf(
			"deployment %q not found",
			deploymentID,
		)

}

func deploymentStatus(ctx context.Context, dockerClient *client.Client, deploymentID string) error {
	containerID, err := findDeployment(ctx, dockerClient, deploymentID)

	if err != nil {
		return err
	}

	status, err := dockerClient.ContainerInspect(
		ctx,
		containerID,
		client.ContainerInspectOptions{},
	)	

 	if err != nil {
        return fmt.Errorf("inspect deployment: %w", err)
    }

    c := status.Container

	fmt.Println("Deployment:", deploymentID)
    fmt.Println("Application:",
        c.Config.Labels["launchpad.application"])
    fmt.Println("Container:", c.ID)
    fmt.Println("Image:", c.Config.Image)
    fmt.Println("Status:", c.State.Status)
    fmt.Println("Running:", c.State.Running)
    fmt.Println("Exit code:", c.State.ExitCode)

	return nil
}

func deploymentLogs(ctx context.Context, dockerClient *client.Client, deploymentID string) error {
	containerID, err := findDeployment(ctx, dockerClient, deploymentID)

	if err != nil {
		return err
	}

	result, err := dockerClient.ContainerInspect(
		ctx,
		containerID,
		client.ContainerInspectOptions{},
	)	

	logs, err := dockerClient.ContainerLogs(
		ctx,
		containerID,
		client.ContainerLogsOptions{
            ShowStdout: true,
            ShowStderr: true,
            Tail:       "100",
            Timestamps: true,
		},
	)
	if err != nil {
		return fmt.Errorf("retrieve logs: %w", err)
	}
	defer logs.Close()

	if result.Container.Config.Tty {
        _, err = io.Copy(os.Stdout, logs)
    } else {
        _, err = stdcopy.StdCopy(
            os.Stdout,
            os.Stderr,
            logs,
        )
    }

    if err != nil {
        return fmt.Errorf("read logs: %w", err)
    }

    return nil
}

func stopDeployment(ctx context.Context, dockerClient *client.Client, deploymentID string) error {
	containerID, err := findDeployment(ctx, dockerClient, deploymentID)

	if err != nil {
		return err
	}

	_, err = dockerClient.ContainerStop(
		ctx,
		containerID,
		client.ContainerStopOptions{},
	)

	if err != nil {
		return fmt.Errorf("stop deployment: %w", err)
    }

    fmt.Println("Deployment stopped:", deploymentID)

    return nil

}

func main() {
    fmt.Println("Launchpad Image Builder")

    if len(os.Args) != 5 || strings.ToLower(os.Args[1]) != "deploy" {
        fmt.Println("Usage:")
        fmt.Println("go run . deploy <directory> <host-port> <app-port>")
        os.Exit(1)
    }

    ctx := context.Background()

    // Connect to Docker Engine.
    dockerClient, err := client.New(client.FromEnv)
    if err != nil {
        log.Fatal("Docker client error: ", err)
    }
    defer dockerClient.Close()

    err = deployApplication(
        ctx,
        dockerClient,
        os.Args[2],
        os.Args[3],
        os.Args[4],
    )

    if err != nil {
        log.Fatal("Deployment failed: ", err)
    }
}
