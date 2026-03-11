package cmd

import (
	"errors"
	"regexp"
)

var workloadNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validateWorkloadName(name string) error {
	if name == "" {
		return errors.New("empty workload")
	}
	if name == "." || name == ".." {
		return errors.New("invalid workload name")
	}
	if !workloadNamePattern.MatchString(name) {
		return errors.New("invalid workload name")
	}
	return nil
}
