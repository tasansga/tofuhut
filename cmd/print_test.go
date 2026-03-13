package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"tofuhut/internal/files"
)

func TestPrintEmbedded(t *testing.T) {
	cases := []struct {
		name string
		file string
	}{
		{name: "server-systemd-service", file: "tofuhut-server.service"},
		{name: "systemd-service", file: "tofuhut-workload@.service"},
		{name: "systemd-timer", file: "tofuhut-workload@.timer"},
		{name: "workload-env", file: "tofuhut-workload.env"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := printEmbedded(&out, tc.file)
			assert.NoError(t, err)

			expected, err := files.Read(tc.file)
			assert.NoError(t, err)
			assert.Equal(t, expected, out.String())
		})
	}
}
