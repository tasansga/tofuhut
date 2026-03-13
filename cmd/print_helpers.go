package cmd

import (
	"fmt"
	"io"

	"tofuhut/internal/files"
)

func printEmbedded(w io.Writer, name string) error {
	content, err := files.Read(name)
	if err != nil {
		return fmt.Errorf("read embedded file %s: %w", name, err)
	}
	_, err = io.WriteString(w, content)
	return err
}
