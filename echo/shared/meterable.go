package shared

import "context"

// Meterable is implemented by any component that can report resource utilization.
// The ResourceManager queries registered Meterables on a cron interval and emits snapshots.
type Meterable interface {
	// GetUtilization returns the current absolute utilization.
	// Returns a slice of Utilization entries (not aggregated - billing service handles aggregation).
	GetUtilization(ctx context.Context) []*Utilization

	// ResourceInfo returns metadata about this resource for the snapshot.
	ResourceInfo() ResourceInfo
}

// ResourceInfo contains metadata about a metered resource.
type ResourceInfo struct {
	// Entity is the fully qualified name to identify the service/capability.
	// e.g., "cre-mainline.wf-zone-a.echocap.filestore"
	Entity string

	// Resource is the specific resource being metered.
	// e.g., "filestore", "vault", "workflow_storage"
	Resource string

	// ResourceType is used by the billing service to convert to universal credits.
	// e.g., "storage_bytes", "compute_seconds", "network_bytes"
	ResourceType string
}

