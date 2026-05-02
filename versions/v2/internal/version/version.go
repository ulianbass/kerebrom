package version

import "fmt"

var (
	Version   = "v2.1.6"
	Commit    = "none"
	BuildDate = "unknown"
)

func Full() string {
	return fmt.Sprintf("%s (commit=%s, build_date=%s)", Version, Commit, BuildDate)
}
