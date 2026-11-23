
package main

import (
    "context"
    "fmt"
    "io"
    "log"
    "os"
    "time"

    "github.com/google/uuid"
    "github.com/moby/moby/client"

    "launchpad/internal/deployment"
    "launchpad/internal/runtime"
    "launchpad/internal/runtime/docker"
)

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() error {
    ctx, cancel := context.WithTimeout(
        context.Background(),
        60*time.Second,
    )
    defer cancel()

    dockerClient, err := client.New(client.FromEnv)

    if err != nil {
        return err
    }

    defer dockerClient.Close()

    dockerRuntime := docker.New(dockerClient)

    executor := deployment.NewExecutor(dockerRuntime)

    req := runtime.DeployRequest{
        JobID:         uuid.NewString(),
        ApplicationID: uuid.NewString(),
        ReleaseID:     uuid.NewString(),
        ImageRef:      "nginx:alpine",
        ContainerPort: 80,
        HostPort:      8090,
    }

    instance, err := executor.Execute(ctx, req)

    if err != nil {
        if instance.ID != "" {
            fmt.Println("Container ID:", instance.ID)
        }

        return err
    }

    fmt.Println("Deployment successful!")
    fmt.Println("Container ID:", instance.ID)
    fmt.Println("Application: http://localhost:8090")

    status, err := dockerRuntime.Inspect(
        ctx, instance.ID,
    )

    if err != nil {
        return err
    }

    fmt.Printf("Container status: %+v\n", status)

    logs, err := dockerRuntime.Logs(
        ctx, instance.ID,
    )

    if err != nil {
        return err
    }

    defer logs.Close()

    fmt.Println("Container logs:")

    _, err = io.Copy(os.Stdout, logs)

    return err
}
