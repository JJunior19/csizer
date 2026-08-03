package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	databasePathEnv = "CONTAINERSIZE_DB_PATH"
	configPathEnv   = "CONTAINERSIZE_CONFIG_PATH"
	dataHomeEnv     = "XDG_DATA_HOME"
	configHomeEnv   = "XDG_CONFIG_HOME"
)

// Paths contains the persistent file locations used by ContainerSize.
type Paths struct {
	Database string
	Config   string
}

// ResolvePaths resolves paths for the current operating system and environment.
func ResolvePaths() (Paths, error) {
	return resolvePaths(runtime.GOOS, os.LookupEnv, os.UserHomeDir)
}

func resolvePaths(
	goos string,
	lookupEnv func(string) (string, bool),
	userHomeDir func() (string, error),
) (Paths, error) {
	var paths Paths

	databaseOverridden, err := resolveOverride(lookupEnv, databasePathEnv, &paths.Database)
	if err != nil {
		return Paths{}, err
	}
	configOverridden, err := resolveOverride(lookupEnv, configPathEnv, &paths.Config)
	if err != nil {
		return Paths{}, err
	}

	home := lazyHome(userHomeDir)

	switch goos {
	case "darwin":
		if databaseOverridden && configOverridden {
			return paths, nil
		}

		homeDirectory, err := home()
		if err != nil {
			return Paths{}, err
		}
		base := filepath.Join(homeDirectory, "Library", "Application Support", "ContainerSize")
		if !databaseOverridden {
			paths.Database = filepath.Join(base, "containersize.db")
		}
		if !configOverridden {
			paths.Config = filepath.Join(base, "config.yaml")
		}
	case "linux":
		if !databaseOverridden {
			dataHome := nonEmptyEnvironment(lookupEnv, dataHomeEnv)
			if dataHome == "" {
				homeDirectory, err := home()
				if err != nil {
					return Paths{}, err
				}
				dataHome = filepath.Join(homeDirectory, ".local", "share")
			}
			paths.Database = filepath.Join(dataHome, "containersize", "containersize.db")
		}
		if !configOverridden {
			configHome := nonEmptyEnvironment(lookupEnv, configHomeEnv)
			if configHome == "" {
				homeDirectory, err := home()
				if err != nil {
					return Paths{}, err
				}
				configHome = filepath.Join(homeDirectory, ".config")
			}
			paths.Config = filepath.Join(configHome, "containersize", "config.yaml")
		}
	default:
		return Paths{}, fmt.Errorf("resolve ContainerSize paths: unsupported operating system %q", goos)
	}

	return paths, nil
}

func resolveOverride(lookupEnv func(string) (string, bool), name string, destination *string) (bool, error) {
	value, set := lookupEnv(name)
	if !set {
		return false, nil
	}
	if value == "" {
		return false, fmt.Errorf("resolve ContainerSize paths: %s is set but empty", name)
	}

	*destination = value
	return true, nil
}

func nonEmptyEnvironment(lookupEnv func(string) (string, bool), name string) string {
	value, set := lookupEnv(name)
	if !set || value == "" {
		return ""
	}
	return value
}

func lazyHome(userHomeDir func() (string, error)) func() (string, error) {
	var (
		resolved bool
		home     string
		err      error
	)

	return func() (string, error) {
		if !resolved {
			resolved = true
			home, err = userHomeDir()
			if err != nil {
				err = fmt.Errorf("resolve ContainerSize paths: home directory: %w", err)
			} else if home == "" {
				err = fmt.Errorf("resolve ContainerSize paths: home directory is empty")
			}
		}
		return home, err
	}
}
