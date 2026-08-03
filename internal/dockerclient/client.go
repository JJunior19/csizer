package dockerclient

import (
	"context"
	"sort"
	"strings"

	"github.com/moby/moby/client"
)

// Container is the minimal Docker metadata needed for discovery and identity.
type Container struct {
	ID     string
	Name   string
	Image  string
	State  string
	Status string
	Labels map[string]string
}

// Client adapts the Moby SDK without exposing its types to consumers.
type Client struct {
	apiClient *client.Client
}

// New constructs a Docker client from the standard Docker environment.
func New(userAgent string) (*Client, error) {
	apiClient, err := client.New(client.FromEnv, client.WithUserAgent(userAgent))
	if err != nil {
		return nil, err
	}
	return &Client{apiClient: apiClient}, nil
}

// List returns all Docker containers, including stopped containers.
func (docker *Client) List(ctx context.Context) ([]Container, error) {
	result, err := docker.apiClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}

	containers := make([]Container, 0, len(result.Items))
	for _, item := range result.Items {
		labels := make(map[string]string, len(item.Labels))
		for key, value := range item.Labels {
			labels[key] = value
		}
		containers = append(containers, Container{
			ID:     item.ID,
			Name:   primaryName(item.Names),
			Image:  item.Image,
			State:  string(item.State),
			Status: item.Status,
			Labels: labels,
		})
	}
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Name == containers[j].Name {
			return containers[i].ID < containers[j].ID
		}
		return containers[i].Name < containers[j].Name
	})

	return containers, nil
}

// Close releases idle connections held by the Docker client.
func (docker *Client) Close() error {
	return docker.apiClient.Close()
}

func primaryName(names []string) string {
	cleaned := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimPrefix(strings.TrimSpace(name), "/")
		if name != "" {
			cleaned = append(cleaned, name)
		}
	}
	sort.Strings(cleaned)
	if len(cleaned) == 0 {
		return ""
	}
	return cleaned[0]
}
