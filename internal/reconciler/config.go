package reconciler

// Config holds resolved runtime settings for a workload run.
type Config struct {
	Mode        string
	Upgrade     bool
	Reconfigure bool
	GatusURL    string
	GatusToken  string
}
