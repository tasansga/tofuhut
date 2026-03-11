package reconciler

// Config holds resolved runtime settings for a workload run.
type Config struct {
	Mode         string
	Upgrade      bool
	Reconfigure  bool
	GatusURL     string
	GatusToken   string
	NtfyURL      string
	NtfyTopic    string
	NtfyToken    string
	ApproveURL   string
	ApproveToken string
}

// ConfigLocks marks fields explicitly set by CLI flags.
// Locked fields must not be overridden by workload env files.
type ConfigLocks struct {
	Mode         bool
	Upgrade      bool
	Reconfigure  bool
	GatusURL     bool
	GatusToken   bool
	NtfyURL      bool
	NtfyTopic    bool
	NtfyToken    bool
	ApproveURL   bool
	ApproveToken bool
}
