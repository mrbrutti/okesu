package ports

import "errors"

// Sentinel errors that adapters return so callers can branch on
// behaviour without sniffing adapter-specific error types.
//
// Adapters wrap the sentinel with context using fmt.Errorf("%w: ...", ErrFoo)
// so errors.Is keeps working.

// ErrNotFound is returned by Get/Read methods when the requested
// resource doesn't exist. Distinct from "permission denied" or "I/O
// failed" — callers commonly translate to HTTP 404.
var ErrNotFound = errors.New("not found")

// ErrAlreadyExists is returned by Create methods that would overwrite
// an existing resource when the adapter doesn't allow that.
var ErrAlreadyExists = errors.New("already exists")

// ErrNotSupported is returned by adapter methods that aren't implemented
// by the chosen backend (e.g. CertManager.Revoke on a CA without a CRL).
// Callers should degrade gracefully rather than fail outright.
var ErrNotSupported = errors.New("not supported by this adapter")

// ErrShutdown is returned by long-lived methods (Subscribe loops, etc.)
// when the adapter is closed mid-operation.
var ErrShutdown = errors.New("adapter shut down")
