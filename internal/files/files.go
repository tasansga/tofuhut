package files

import "embed"

//go:embed tofuhut-workload@.service tofuhut-workload@.timer tofuhut-workload.env tofuhut-server.service
var fs embed.FS

// Read returns the embedded file content.
func Read(name string) (string, error) {
	b, err := fs.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
