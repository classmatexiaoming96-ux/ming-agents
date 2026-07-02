package memory

import "errors"

// Typed resolve/curation errors. Surfaces (HTTP API, CLI) classify failures by
// kind via errors.Is instead of matching on message text, which is brittle when
// wording changes. Each sentinel groups a class of failures that map to one
// HTTP status; the wrapped errors keep their descriptive message for humans.
var (
	// ErrNotFound: a referenced memory does not exist. (HTTP 404)
	ErrNotFound = errors.New("memory not found")

	// ErrMemberNotActive: a pair member exists but is not active, so it cannot
	// participate in a contradiction resolution. (HTTP 404)
	ErrMemberNotActive = errors.New("memory not active")

	// ErrNotPendingContradiction: the requested pair is not a pending
	// contradiction the detector surfaced. (HTTP 422)
	ErrNotPendingContradiction = errors.New("not a pending contradiction")

	// ErrUnboundedBatch: an apply batch is unbounded or exceeds the cap without
	// the explicit override. (HTTP 422)
	ErrUnboundedBatch = errors.New("resolve batch refused")

	// ErrInvalidTransition: a promotion state-machine edge is not allowed.
	// (HTTP 409)
	ErrInvalidTransition = errors.New("promotion transition not allowed")

	// ErrL1Supersede: an L1 memory cannot be superseded via contradiction; it
	// must be curated or revoked explicitly. (HTTP 409)
	ErrL1Supersede = errors.New("l1 memory must be curated or revoked explicitly")

	// ErrValidation: a request is missing required inputs or combines mutually
	// exclusive ones. (HTTP 400)
	ErrValidation = errors.New("invalid request")
)
