package main

import (
	"os"

	"tofuhut/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		if exitErr, ok := err.(*cmd.ExitCodeError); ok {
			os.Exit(exitErr.Code)
		}
		os.Exit(1)
	}
}
