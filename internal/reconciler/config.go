package reconciler

import "time"

// Config holds resolved runtime settings for a workload run.
type Config struct {
	WorkloadType         string
	Mode                 string
	PreReconcileHook     string
	PostReconcileHook    string
	PreHookTimeout       time.Duration
	PostHookTimeout      time.Duration
	ReconcileChangedOnly bool
	Upgrade              bool
	Reconfigure          bool
	GatusURL             string
	GatusToken           string
	NtfyURL              string
	NtfyTopic            string
	NtfyToken            string
	ApproveURL           string
	WorkloadToken        string
}

// ConfigLocks marks fields explicitly set by CLI flags.
// Locked fields must not be overridden by workload env files.
type ConfigLocks struct {
	WorkloadType         bool
	Mode                 bool
	PreReconcileHook     bool
	PostReconcileHook    bool
	PreHookTimeout       bool
	PostHookTimeout      bool
	ReconcileChangedOnly bool
	Upgrade              bool
	Reconfigure          bool
	GatusURL             bool
	GatusToken           bool
	NtfyURL              bool
	NtfyTopic            bool
	NtfyToken            bool
	ApproveURL           bool
	WorkloadToken        bool
}
