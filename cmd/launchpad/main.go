package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type APIClient struct {
	baseURL string
	http    *http.Client
	token   string
}

func newAPIClient(baseURL string) *APIClient {
	return &APIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
		token: os.Getenv("LAUNCHPAD_DEPLOY_TOKEN"),
	}
}

func (c *APIClient) request(ctx context.Context, method string, path string, body any, result any) error {
	var reader io.Reader

	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		method,
		c.baseURL+path,
		reader,
	)
	if err != nil {
		return err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(
			io.LimitReader(resp.Body, 4096),
		)
		return fmt.Errorf(
			"API returned %s: %s",
			resp.Status,
			strings.TrimSpace(string(data)),
		)
	}

	if result == nil {
		return nil
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

type Application struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Release struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type Deployment struct {
	ID           string  `json:"id"`
	ReleaseID    string  `json:"release_id"`
	Status       string  `json:"status"`
	ContainerID  *string `json:"container_id"`
	ErrorMessage *string `json:"error_message"`
	URL          string  `json:"url"`
}

func main() {
	baseURL := os.Getenv("LAUNCHPAD_API_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8081"
	}

	client := newAPIClient(baseURL)

	root := &cobra.Command{
		Use:          "launchpad",
		Short:        "Deploy and manage applications with Launchpad",
		SilenceUsage: true,
	}

	appsCmd := &cobra.Command{
		Use:   "apps",
		Short: "Manage applications",
	}

	appsCreateCmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an application",
		Args:  cobra.ExactArgs(1),

		RunE: func(cmd *cobra.Command, args []string) error {
			var app Application

			err := client.request(
				cmd.Context(),
				http.MethodPost,
				"/applications",
				map[string]string{"name": args[0]},
				&app,
			)

			if err != nil {
				return err
			}

			fmt.Printf(
				"Created application: %s\nID: %s\n",
				app.Name, app.ID,
			)
			return nil
		},
	}

	appsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List applications",
		RunE: func(cmd *cobra.Command, args []string) error {
			var apps []Application

			err := client.request(
				cmd.Context(),
				http.MethodGet,
				"/applications",
				nil,
				&apps,
			)

			if err != nil {
				return err
			}

			for _, app := range apps {
				fmt.Printf("%s\t%s\n", app.ID, app.Name)
			}
			return nil
		},
	}

	appsCmd.AddCommand(appsCreateCmd, appsListCmd)

	var appID string
	var imageRef string
	var port int

	deployCmd := &cobra.Command{
		Use:   "deploy",
		Short: "Create a release and queue deployment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if port < 1 || port > 65535 {
				return fmt.Errorf("port mus tbe ebtween 1 and 65535")
			}

			var release Release

			err := client.request(
				cmd.Context(),
				http.MethodPost,
				"/applications/"+appID+"/releases",
				map[string]any{
					"image_ref":      imageRef,
					"container_port": port,
				},
				&release,
			)
			if err != nil {
				return fmt.Errorf("create release: %w", err)
			}

			fmt.Printf(
				"Created release v%d: %s\n",
				release.Version, release.ID,
			)

			var job Deployment

			err = client.request(
				cmd.Context(),
				http.MethodPost,
				"/releases/"+release.ID+"/deployments",
				nil,
				&job,
			)

			if err != nil {
				return fmt.Errorf(
					"release created but deployment "+
						"request failed: %w",
					err,
				)
			}
			fmt.Println("Deployment queued!")
			fmt.Println("Job ID:", job.ID)
			fmt.Println("Status:", job.Status)
			fmt.Println(
				"Check with: launchpad status " + job.ID,
			)

			return nil
		},
	}

	deployCmd.Flags().StringVar(
		&appID, "app", "", "Application UUID",
	)

	deployCmd.Flags().StringVar(
		&imageRef, "image", "", "Docker image reference",
	)

	deployCmd.Flags().IntVar(
		&port, "port", 80, "Port",
	)

	_ = deployCmd.MarkFlagRequired("app")
	_ = deployCmd.MarkFlagRequired("image")

	statusCmd := &cobra.Command{
		Use:   "status DEPLOYMENT_ID",
		Short: "Inspect a deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var job Deployment

			err := client.request(
				cmd.Context(),
				http.MethodGet,
				"/deployments/"+args[0],
				nil,
				&job,
			)

			if err != nil {
				return err
			}
			fmt.Println("Deployment:", job.ID)
			fmt.Println("Status:", job.Status)

			if job.ContainerID != nil {
				fmt.Println("Container:", *job.ContainerID)
			}

			if job.URL != "" {
				fmt.Println("URL:", job.URL)
			}

			if job.ErrorMessage != nil {
				fmt.Println("Error:", *job.ErrorMessage)
			}

			return nil
		},
	}

	root.AddCommand(appsCmd, deployCmd, statusCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
