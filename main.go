
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
                totalSize = info.Size()
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
	if err != nil || port < 0 || port > 65536 {
        return fmt.Errorf("invalid port: %s", value)
	}
	return nil
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
