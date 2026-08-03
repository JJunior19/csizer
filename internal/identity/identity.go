package identity

import (
	"fmt"
	"strings"
)

const (
	LabelWorkload               = "containersize.workload"
	LabelComposeProject         = "com.docker.compose.project"
	LabelComposeService         = "com.docker.compose.service"
	LabelComposeContainerNumber = "com.docker.compose.container-number"
)

// Source identifies the metadata selected for a workload identity.
type Source string

const (
	SourceWorkloadLabel Source = "workload-label"
	SourceCompose       Source = "compose"
	SourceAlias         Source = "alias"
	SourceContainerName Source = "container-name"
	SourceImage         Source = "image-repository"
)

// Container contains only metadata used by workload identity resolution.
type Container struct {
	Name   string
	Image  string
	Labels map[string]string
}

// Result is a stable workload identity and its supporting metadata.
type Result struct {
	WorkloadKey            string
	DisplayName            string
	Source                 Source
	ComposeProject         string
	ComposeService         string
	ComposeContainerNumber string
	ImageRepository        string
}

// Resolve selects a workload identity without performing I/O.
func Resolve(container Container, alias string) (Result, error) {
	normalizedAlias := ""
	if alias != "" {
		var err error
		normalizedAlias, err = normalize(alias)
		if err != nil {
			return Result{}, fmt.Errorf("invalid alias %q: %w", alias, err)
		}
	}

	rawProject := strings.TrimSpace(container.Labels[LabelComposeProject])
	rawService := strings.TrimSpace(container.Labels[LabelComposeService])

	result := Result{
		ComposeProject:         normalizedMetadata(rawProject),
		ComposeService:         normalizedMetadata(rawService),
		ComposeContainerNumber: normalizedMetadata(container.Labels[LabelComposeContainerNumber]),
		ImageRepository:        imageRepository(container.Image),
	}

	if workload, present := container.Labels[LabelWorkload]; present {
		normalized, normalizeErr := normalize(workload)
		if normalizeErr != nil {
			return Result{}, fmt.Errorf("invalid %s label %q: %w", LabelWorkload, workload, normalizeErr)
		}
		return resolved(result, normalized, SourceWorkloadLabel), nil
	}

	if rawProject != "" && rawService != "" {
		project, projectErr := normalizeLabel(container.Labels, LabelComposeProject)
		if projectErr != nil {
			return Result{}, projectErr
		}
		service, serviceErr := normalizeLabel(container.Labels, LabelComposeService)
		if serviceErr != nil {
			return Result{}, serviceErr
		}
		result.ComposeProject = project
		result.ComposeService = service
		return resolved(result, project+"/"+service, SourceCompose), nil
	}

	if normalizedAlias != "" {
		return resolved(result, normalizedAlias, SourceAlias), nil
	}

	if strings.TrimSpace(container.Name) != "" {
		normalized, normalizeErr := normalize(container.Name)
		if normalizeErr != nil {
			return Result{}, fmt.Errorf("invalid container name %q: %w", container.Name, normalizeErr)
		}
		return resolved(result, normalized, SourceContainerName), nil
	}

	if result.ImageRepository != "" {
		return resolved(result, result.ImageRepository, SourceImage), nil
	}

	return Result{}, fmt.Errorf("workload identity is unavailable: no valid label, Compose service, alias, container name, or image repository")
}

func resolved(result Result, key string, source Source) Result {
	result.WorkloadKey = key
	result.DisplayName = key[strings.LastIndex(key, "/")+1:]
	result.Source = source
	return result
}

func normalizeLabel(labels map[string]string, key string) (string, error) {
	value := strings.TrimSpace(labels[key])
	if value == "" {
		return "", nil
	}
	normalized, err := normalize(value)
	if err != nil {
		return "", fmt.Errorf("invalid %s label %q: %w", key, value, err)
	}
	return normalized, nil
}

func normalizedMetadata(value string) string {
	normalized, err := normalize(value)
	if err != nil {
		return ""
	}
	return normalized
}

func normalize(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		return "", fmt.Errorf("value is empty")
	}

	for _, segment := range strings.Split(value, "/") {
		if err := validateSegment(segment); err != nil {
			return "", err
		}
	}
	return value, nil
}

func validateSegment(segment string) error {
	if segment == "" || segment == "." || segment == ".." {
		return fmt.Errorf("value contains unsafe segment %q", segment)
	}
	hasLetterOrDigit := false
	for _, character := range segment {
		switch {
		case character >= 'a' && character <= 'z':
			hasLetterOrDigit = true
		case character >= '0' && character <= '9':
			hasLetterOrDigit = true
		case character == '.', character == '_', character == '-':
		default:
			return fmt.Errorf("value contains unsafe character %q", character)
		}
	}
	if !hasLetterOrDigit {
		return fmt.Errorf("segment %q has no letter or digit", segment)
	}
	return nil
}

func imageRepository(image string) string {
	reference := strings.ToLower(strings.TrimSpace(image))
	if reference == "" || isBareSHA256(reference) {
		return ""
	}
	if repository, _, found := strings.Cut(reference, "@"); found {
		reference = repository
	}
	lastSlash := strings.LastIndex(reference, "/")
	if lastColon := strings.LastIndex(reference, ":"); lastColon > lastSlash {
		reference = reference[:lastColon]
	}
	if reference == "" || strings.HasPrefix(reference, "/") || strings.HasSuffix(reference, "/") {
		return ""
	}

	segments := strings.Split(reference, "/")
	for index, segment := range segments {
		if index == 0 && len(segments) > 1 && strings.Contains(segment, ":") {
			host, port, found := strings.Cut(segment, ":")
			if !found || strings.Contains(port, ":") || validateSegment(host) != nil || !digits(port) {
				return ""
			}
			continue
		}
		if validateSegment(segment) != nil {
			return ""
		}
	}
	return reference
}

func isBareSHA256(reference string) bool {
	digest, found := strings.CutPrefix(reference, "sha256:")
	if !found || len(digest) < 12 {
		return false
	}
	for _, character := range digest {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func digits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
