package controlplane

// buildVersion is overridden at build time via:
//
//	-ldflags "-X github.com/section9labs/okesu/controlplane.buildVersion=<version>"
//
// Both the daemon and the CP read their respective package-level
// buildVersion at startup and include it in heartbeats / boot logs /
// /api/system/about so operators can tell which version is running
// where.
var buildVersion = "dev"

// Version returns the okesu-cp binary version string.
func Version() string { return buildVersion }
