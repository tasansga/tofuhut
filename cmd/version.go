package cmd

import "strings"

var (
	version = "dev"
	commit  = "unknown"
)

func buildVersion() string {
	v := strings.TrimSpace(version)
	c := strings.TrimSpace(commit)
	if v == "" {
		v = "dev"
	}
	if c == "" || c == "unknown" {
		return v
	}
	return v + " (" + c + ")"
}
