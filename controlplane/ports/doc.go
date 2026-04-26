// Package ports defines the interfaces ("ports" in hexagonal-architecture
// terms) that the Control Plane uses to talk to external systems —
// databases, queues, pub/sub, blob storage, PKI, secrets stores.
//
// Each interface lives in its own file. Implementations live under
// controlplane/adapters/<technology>/. Selecting an adapter is a config
// decision (--queue=kafka vs --queue=inprocess); the rest of the CP
// holds an interface and never imports an adapter directly.
//
// Why this exists
//
// We want the same Control Plane code to:
//   - Run in dev with a single docker-compose stack (Postgres, ClickHouse,
//     Redpanda, Redis on the laptop)
//   - Run in production on OCI using OCI Database PostgreSQL, OCI
//     Streaming, OCI Cache, OCI Object Storage, OCI Certificates, OCI
//     Vault — without code changes
//   - Run on other clouds (AWS RDS + MSK + ElastiCache + S3 + ACM PCA +
//     KMS) by swapping adapters
//
// The interfaces are deliberately minimal. They model what the CP
// genuinely needs, not the full surface area of any particular vendor.
// New methods are added when a CP feature requires them, not eagerly.
//
// Design rules
//
//   - Interfaces are small. Most have 1-5 methods. Where we need many
//     methods (e.g. relational state), we segregate by concern.
//   - Errors are wrapped with adapter-specific context but the interface
//     callers don't sniff the error type.
//   - Context is the first arg of every method. No exceptions.
//   - Adapters live in their own packages so a build flag or build tag
//     could exclude them; vendor SDKs stay out of the core CP module
//     graph.
//   - Tests against the interface use the inprocess adapter as a stand-in
//     for the real thing. No mocks.
package ports
