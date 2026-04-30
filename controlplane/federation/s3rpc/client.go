// Parent-side write-pipe client. Submit blocks until the child's
// reply lands or the timeout expires.
//
// Usage from a forwarding handler:
//
//	cli := s3rpc.NewClient(s3client, parentID, peer.BucketPrefix)
//	resp, err := cli.Submit(ctx, s3rpc.Request{Kind: "create_node", Body: bodyJSON})
//
// One Client per (peer, bucket) combination is fine to reuse across
// concurrent submits — internal state is per-Submit and there's no
// shared mutable state to lock.

package s3rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Client submits directives to a child CP via the bucket and polls
// for the response.
type Client struct {
	cli          Bucket
	reqPrefix    string // 'cp/<parent>/outbound/<child>/req/'
	respPrefix   string // 'cp/<child>/outbound/<parent>/resp/'
	pollInterval time.Duration
	timeout      time.Duration
}

// NewClient wires up a Client against an existing s3transport
// connection (or any Bucket implementation). parentSelfPrefix is
// `cp/<parent-id>/` (where the parent writes from); peerOutboundPrefix
// is the same value the federation_peers row carries (where the child
// publishes — i.e. `cp/<child-id>/outbound/<parent-id>/`). The Client
// derives both req + resp prefixes from those.
//
// The directive direction (parent → child) lives at:
//
//	cp/<parent-id>/outbound/<child-id>/req/<id>.json
//
// We compute it by reversing the peerOutboundPrefix: the bucket
// convention `cp/<self>/outbound/<peer>/` is symmetric, so swapping
// self and peer is sufficient.
func NewClient(cli Bucket, parentSelfID, peerOutboundPrefix string) (*Client, error) {
	if cli == nil {
		return nil, errors.New("s3rpc: client requires non-nil Bucket")
	}
	parentSelfID = strings.Trim(parentSelfID, "/")
	if parentSelfID == "" {
		return nil, errors.New("s3rpc: parentSelfID required")
	}
	peerID := childIDFromOutboundPrefix(peerOutboundPrefix)
	if peerID == "" {
		return nil, fmt.Errorf("s3rpc: cannot derive peer id from prefix %q", peerOutboundPrefix)
	}
	return &Client{
		cli:          cli,
		reqPrefix:    fmt.Sprintf("cp/%s/outbound/%s/req/", parentSelfID, peerID),
		respPrefix:   fmt.Sprintf("cp/%s/outbound/%s/resp/", peerID, parentSelfID),
		pollInterval: DefaultClientPollInterval,
		timeout:      DefaultClientTimeout,
	}, nil
}

// Submit writes the request to the bucket, polls for the matching
// response, and returns it. RequestID is filled in if the caller
// left it empty.
func (c *Client) Submit(ctx context.Context, req Request) (*Response, error) {
	if req.Kind == "" {
		return nil, errors.New("s3rpc.Submit: kind is required")
	}
	if req.RequestID == "" {
		req.RequestID = uuid.NewString()
	}
	if req.IssuedAt.IsZero() {
		req.IssuedAt = time.Now().UTC()
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	reqKey := c.reqPrefix + req.RequestID + ".json"
	if err := c.cli.Put(ctx, reqKey, body, "application/json"); err != nil {
		return nil, fmt.Errorf("put req: %w", err)
	}

	// Poll for resp/<id>.json. Bounded by the per-Submit timeout —
	// a long-running directive (deploy, run-agent) is expected to
	// return a "started" response with a job id quickly, then the
	// operator follows progress via the federated reads (Phase A).
	respKey := c.respPrefix + req.RequestID + ".json"
	pollCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	t := time.NewTicker(c.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-pollCtx.Done():
			return nil, fmt.Errorf("timed out after %s waiting for %s response (request %s)",
				c.timeout, req.Kind, req.RequestID)
		case <-t.C:
			respBody, err := c.cli.GetBytes(pollCtx, respKey)
			if err != nil {
				// Most likely 404 — keep polling. Other errors
				// (auth, network) will keep failing the same way
				// until the per-Submit timeout fires; logging them
				// here would just be noise.
				continue
			}
			if len(respBody) == 0 {
				continue
			}
			var resp Response
			if err := json.Unmarshal(respBody, &resp); err != nil {
				return nil, fmt.Errorf("parse resp: %w", err)
			}
			// Best-effort cleanup so the bucket doesn't grow
			// without bound. Failure here is log-only — the
			// next poll cycle on either side will reconcile.
			_ = c.cli.Delete(pollCtx, reqKey)
			_ = c.cli.Delete(pollCtx, respKey)
			return &resp, nil
		}
	}
}

// childIDFromOutboundPrefix peels the child id out of a string like
// 'cp/<child>/outbound/<parent>/'. Returns "" on malformed input.
// The function is the same shape on both ends — used here to derive
// "where to read responses from" given "where the child publishes."
func childIDFromOutboundPrefix(prefix string) string {
	prefix = strings.Trim(prefix, "/")
	parts := strings.Split(prefix, "/")
	// Expect: ["cp", "<child>", "outbound", "<parent>"]
	if len(parts) < 4 || parts[0] != "cp" || parts[2] != "outbound" {
		return ""
	}
	return parts[1]
}
