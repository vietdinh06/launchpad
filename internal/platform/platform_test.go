package platform

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    string
		wantErr bool
	}{
		{"HTTP", "80", false},
		{"HTTPS", "443", false},
		{"Typical app", "8080", false},
		{"Minimum", "1", false},
		{"Maximum", "65535", false},
		{"Zero", "0", true},
		{"Too large", "65536", true},
		{"Negative", "-1", true},
		{"Text", "hello", true},
		{"Empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePort(tt.port)

			gotErr := err != nil

			if gotErr != tt.wantErr {
				t.Errorf(
					"validatePort(%q) error = %v, wantErr = %v",
					tt.port,
					err,
					tt.wantErr,
				)
			}
		})
	}
}

func TestCreateBuildContext(t *testing.T) {
	// Create an isolated temporary application directory.
	appPath := t.TempDir()

	// Create a Dockerfile.
	dockerfile := []byte("FROM nginx:alpine\n")

	err := os.WriteFile(
		filepath.Join(appPath, "Dockerfile"),
		dockerfile,
		0644,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Create a source file.
	err = os.WriteFile(
		filepath.Join(appPath, "index.html"),
		[]byte("<h1>Hello Launchpad</h1>"),
		0644,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Create a file we should exclude.
	err = os.WriteFile(
		filepath.Join(appPath, ".env"),
		[]byte("SECRET=test-value"),
		0600,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Call our existing build-context function.
	buffer, err := createBuildContext(appPath)
	if err != nil {
		t.Fatal(err)
	}

	// Read the generated tar archive.
	reader := tar.NewReader(buffer)

	files := make(map[string]bool)

	for {
		header, err := reader.Next()

		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}

		files[header.Name] = true
	}

	// Verify required files were included.
	if !files["Dockerfile"] {
		t.Error("Dockerfile missing from build context")
	}

	if !files["index.html"] {
		t.Error("index.html missing from build context")
	}

	// Verify excluded files were omitted.
	if files[".env"] {
		t.Error(".env should not be included")
	}
}

func TestBuildContextSizeLimit(t *testing.T) {
	appPath := t.TempDir()

	// A file exceeding the 25 MiB limit.
	largeFile := make([]byte, maxContextSize+1)

	err := os.WriteFile(
		filepath.Join(appPath, "large.bin"),
		largeFile,
		0644,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = createBuildContext(appPath)

	if err == nil {
		t.Fatal("expected an error for oversized context")
	}
}
