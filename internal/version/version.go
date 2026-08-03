package version

import "fmt"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// BuildInfo contains values that release builds can inject with -ldflags -X.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Current returns the build metadata compiled into the executable.
func Current() BuildInfo {
	return BuildInfo{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
	}
}

func (info BuildInfo) String() string {
	return fmt.Sprintf("version=%s commit=%s date=%s", info.Version, info.Commit, info.Date)
}
