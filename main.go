
package main

import (
    "archive/tar"
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "os"
    "path/filepath"

    "github.com/moby/moby/client"
)

// createBuildContext packages the two files in our
// example application into an in-memory tar archive.
func createBuildContext(appPath string) (*bytes.Buffer, error) {
    var buffer bytes.Buffer
    tarWriter := tar.NewWriter(&buffer)

    files := []string{"Dockerfile", "index.html"}

    for _, name := range files {
        path := filepath.Join(appPath, name)

        data, err := os.ReadFile(path)
        if err != nil {
            return nil, fmt.Errorf("read %s: %w", name, err)
        }

        header := &tar.Header{
            Name: name,
            Mode: 0644,
            Size: int64(len(data)),
        }

        if err := tarWriter.WriteHeader(header); err != nil {
            return nil, err
        }

        if _, err := tarWriter.Write(data); err != nil {
            return nil, err
        }
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

    var event struct {
        Stream string `json:"stream"`
        Error  string `json:"error"`
    }

    for {
        event = struct {
            Stream string `json:"stream"`
            Error  string `json:"error"`
        }{}

        if err := decoder.Decode(&event); err != nil {
            if err == io.EOF {
                return nil
            }
            return fmt.Errorf("read build output: %w", err)
        }

        if event.Error != "" {
            return fmt.Errorf("Docker build failed: %s", event.Error)
        }

        if event.Stream != "" {
            fmt.Print(event.Stream)
        }
    }
}

func main() {
    fmt.Println("Launchpad Image Builder")

    ctx := context.Background()

    // Connect to Docker Engine.
    dockerClient, err := client.New(client.FromEnv)
    if err != nil {
        log.Fatal("Docker client error: ", err)
    }
    defer dockerClient.Close()

    // Package our example application's source files.
    appPath := "./examples/hello-web"

    buildContext, err := createBuildContext(appPath)
    if err != nil {
        log.Fatal("Build context error: ", err)
    }

    fmt.Println("Build context prepared.")

    // Request Docker to build the image.
    imageName := "launchpad-hello:v1"

    result, err := dockerClient.ImageBuild(
        ctx,
        buildContext,
        client.ImageBuildOptions{
            Tags: []string{imageName},
        },
    )
    if err != nil {
        log.Fatal("Build request failed: ", err)
    }
    defer result.Body.Close()

    // Read build progress and detect failures.
    if err := readBuildOutput(result.Body); err != nil {
        log.Fatal(err)
    }

    // Verify that Docker has the expected image.
    _, err = dockerClient.ImageInspect(
        ctx,
        imageName,
    )
    if err != nil {
        log.Fatal("Image verification failed: ", err)
    }

    fmt.Println()
    fmt.Println("Image built successfully!")
    fmt.Println("Image:", imageName)
}
