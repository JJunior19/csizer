package dockerclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	eventtypes "github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/jorgeccarhuasaroni/containersize/internal/collector"
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

// Ping verifies that the Docker daemon is accessible.
func (docker *Client) Ping(ctx context.Context) error {
	_, err := docker.apiClient.Ping(ctx, client.PingOptions{})
	return err
}

// Stats reads one Docker stats response and maps it to collector-owned counters.
func (docker *Client) Stats(ctx context.Context, containerID string) (stats collector.RuntimeStats, err error) {
	result, err := docker.apiClient.ContainerStats(ctx, containerID, client.ContainerStatsOptions{
		IncludePreviousSample: true,
	})
	if err != nil {
		return collector.RuntimeStats{}, err
	}
	defer func() {
		if closeErr := result.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close Docker stats response: %w", closeErr)
		}
	}()

	var response containertypes.StatsResponse
	if err := json.NewDecoder(result.Body).Decode(&response); err != nil {
		return collector.RuntimeStats{}, fmt.Errorf("decode Docker stats response: %w", err)
	}
	return runtimeStats(response), nil
}

// Events streams only normalized container lifecycle events until ctx is canceled.
func (docker *Client) Events(ctx context.Context) (<-chan collector.LifecycleEvent, <-chan error) {
	filters := make(client.Filters).Add("type", "container")
	result := docker.apiClient.Events(ctx, client.EventsListOptions{Filters: filters})
	messages := make(chan collector.LifecycleEvent)
	errorsChannel := make(chan error, 1)
	go func() {
		defer close(messages)
		defer close(errorsChannel)
		for {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-result.Err:
				if !ok {
					return
				}
				if err != nil {
					errorsChannel <- err
				}
				return
			case message, ok := <-result.Messages:
				if !ok {
					return
				}
				event, found := normalizeEvent(message)
				if !found {
					continue
				}
				select {
				case messages <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return messages, errorsChannel
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

func runtimeStats(response containertypes.StatsResponse) collector.RuntimeStats {
	stats := collector.RuntimeStats{
		Timestamp: response.Read,
		CPU: collector.CPUCounters{
			TotalUsage:  response.CPUStats.CPUUsage.TotalUsage,
			SystemUsage: response.CPUStats.SystemUsage,
			OnlineCPUs:  response.CPUStats.OnlineCPUs,
		},
		Memory: collector.Memory{
			UsageBytes: response.MemoryStats.Usage,
			CacheBytes: memoryCache(response.MemoryStats.Stats),
			LimitBytes: nonzeroUint64(response.MemoryStats.Limit),
		},
		PIDs:       nonzeroUint64(response.PidsStats.Current),
		NetworkRx:  networkBytes(response.Networks, func(network containertypes.NetworkStats) uint64 { return network.RxBytes }),
		NetworkTx:  networkBytes(response.Networks, func(network containertypes.NetworkStats) uint64 { return network.TxBytes }),
		BlockRead:  blockIOBytes(response.BlkioStats.IoServiceBytesRecursive, "read"),
		BlockWrite: blockIOBytes(response.BlkioStats.IoServiceBytesRecursive, "write"),
	}
	if !response.PreRead.IsZero() {
		stats.PreviousCPU = &collector.CPUCounters{
			TotalUsage:  response.PreCPUStats.CPUUsage.TotalUsage,
			SystemUsage: response.PreCPUStats.SystemUsage,
			OnlineCPUs:  response.PreCPUStats.OnlineCPUs,
		}
	}
	return stats
}

func memoryCache(stats map[string]uint64) *uint64 {
	// Docker exposes reclaimable cache as inactive_file on cgroup v2 and as
	// total_inactive_file on cgroup v1.
	if value, found := stats["inactive_file"]; found {
		return &value
	}
	if value, found := stats["total_inactive_file"]; found {
		return &value
	}
	return nil
}

func nonzeroUint64(value uint64) *uint64 {
	if value == 0 {
		return nil
	}
	return &value
}

func networkBytes(networks map[string]containertypes.NetworkStats, value func(containertypes.NetworkStats) uint64) *uint64 {
	if networks == nil {
		return nil
	}
	var total uint64
	for _, network := range networks {
		current := value(network)
		if ^uint64(0)-total < current {
			return nil
		}
		total += current
	}
	return &total
}

func blockIOBytes(entries []containertypes.BlkioStatEntry, operation string) *uint64 {
	if entries == nil {
		return nil
	}
	var total uint64
	for _, entry := range entries {
		if !strings.EqualFold(entry.Op, operation) {
			continue
		}
		if ^uint64(0)-total < entry.Value {
			return nil
		}
		total += entry.Value
	}
	return &total
}

func normalizeEvent(message eventtypes.Message) (collector.LifecycleEvent, bool) {
	var eventType collector.EventType
	switch string(message.Action) {
	case string(collector.EventStart):
		eventType = collector.EventStart
	case string(collector.EventStop):
		eventType = collector.EventStop
	case string(collector.EventDie):
		eventType = collector.EventDie
	case string(collector.EventDestroy):
		eventType = collector.EventDestroy
	case string(collector.EventOOM):
		eventType = collector.EventOOM
	case string(collector.EventRename):
		eventType = collector.EventRename
	default:
		return collector.LifecycleEvent{}, false
	}
	if strings.TrimSpace(message.Actor.ID) == "" || message.TimeNano <= 0 && message.Time <= 0 {
		return collector.LifecycleEvent{}, false
	}
	timestamp := time.Unix(message.Time, 0)
	if message.TimeNano > 0 {
		timestamp = time.Unix(0, message.TimeNano)
	}
	event := collector.LifecycleEvent{
		ContainerID: message.Actor.ID,
		Timestamp:   timestamp.UTC(),
		Type:        eventType,
	}
	if exitCode, err := strconv.ParseInt(strings.TrimSpace(message.Actor.Attributes["exitCode"]), 10, 64); err == nil {
		event.ExitCode = &exitCode
	}
	if eventType == collector.EventRename {
		event.Name = strings.TrimSpace(message.Actor.Attributes["name"])
	}
	return event, true
}
