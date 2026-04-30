// Roundtrip tests exercise s3rpc.Client + s3rpc.Server against a
// shared in-memory bucket. They lock in the wire-level behavior that
// production deployments depend on:
//
//   1. A submitted directive lands as a req object, reaches the
//      registered handler, and the handler's response gets back to
//      the caller untouched.
//   2. Idempotency: re-issuing the same request_id returns the same
//      response without re-running the handler.
//   3. Multi-parent: a child sees req objects from any
//      cp/<parent>/outbound/<self>/req/ prefix, not just one.
//   4. Cleanup: after a successful round trip both req and resp
//      objects are deleted so the bucket doesn't grow without bound.
//   5. Timeout: a missing handler response surfaces as an error in
//      Submit rather than hanging forever.

package s3rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// helper: wire up a Server on bucket bkt that handles one kind. The
// poll interval is shrunk so tests run in milliseconds.
func newTestServer(t *testing.T, bkt Bucket, selfID, kind string, h Handler) *Server {
	t.Helper()
	srv, err := New(bkt, selfID)
	if err != nil {
		t.Fatalf("s3rpc.New: %v", err)
	}
	srv.interval = 20 * time.Millisecond
	srv.Register(kind, h)
	return srv
}

// helper: wire up a Client with shrunk poll interval + timeout.
func newTestClient(t *testing.T, bkt Bucket, parentID, peerOutboundPrefix string) *Client {
	t.Helper()
	c, err := NewClient(bkt, parentID, peerOutboundPrefix)
	if err != nil {
		t.Fatalf("s3rpc.NewClient: %v", err)
	}
	c.pollInterval = 10 * time.Millisecond
	c.timeout = 2 * time.Second
	return c
}

func TestRoundtrip_HappyPath(t *testing.T) {
	bkt := newFakeBucket()
	const parentID, childID = "global", "east"

	// Echo handler returns a body containing the original payload
	// plus a marker so we can verify the operator sees the child's
	// response, not a relayed copy of the request.
	handler := func(ctx context.Context, req Request) Response {
		var in struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Body, &in)
		out, _ := json.Marshal(map[string]any{
			"name":     in.Name,
			"executed": true,
		})
		return Response{HTTPStatus: 200, Body: out}
	}
	srv := newTestServer(t, bkt, childID, "echo", handler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)

	cli := newTestClient(t, bkt, parentID, fmt.Sprintf("cp/%s/outbound/%s/", childID, parentID))
	resp, err := cli.Submit(ctx, Request{
		Kind: "echo",
		Body: json.RawMessage(`{"name":"node-1"}`),
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("Status = %q, want ok (err=%q body=%q)", resp.Status, resp.Error, resp.Body)
	}
	if resp.HTTPStatus != 200 {
		t.Fatalf("HTTPStatus = %d, want 200", resp.HTTPStatus)
	}
	var got struct {
		Name     string `json:"name"`
		Executed bool   `json:"executed"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal resp body: %v", err)
	}
	if got.Name != "node-1" || !got.Executed {
		t.Fatalf("response payload mismatch: got %+v", got)
	}
}

func TestRoundtrip_CleansUpBothObjects(t *testing.T) {
	bkt := newFakeBucket()
	const parentID, childID = "global", "east"

	srv := newTestServer(t, bkt, childID, "noop",
		func(ctx context.Context, req Request) Response {
			return Response{HTTPStatus: 200, Body: json.RawMessage(`{}`)}
		})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)

	cli := newTestClient(t, bkt, parentID, fmt.Sprintf("cp/%s/outbound/%s/", childID, parentID))
	if _, err := cli.Submit(ctx, Request{Kind: "noop", Body: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// After a successful round trip both req and resp objects should
	// have been deleted by the Client's cleanup path.
	for _, k := range bkt.snapshotKeys() {
		if strings.Contains(k, "/req/") || strings.Contains(k, "/resp/") {
			t.Errorf("expected req+resp objects to be deleted, but found %q", k)
		}
	}
}

func TestRoundtrip_IdempotencyRunsHandlerOnce(t *testing.T) {
	bkt := newFakeBucket()
	const parentID, childID = "global", "east"

	var calls atomic.Int64
	srv := newTestServer(t, bkt, childID, "counter",
		func(ctx context.Context, req Request) Response {
			calls.Add(1)
			body, _ := json.Marshal(map[string]int64{"call": calls.Load()})
			return Response{HTTPStatus: 200, Body: body}
		})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)

	cli := newTestClient(t, bkt, parentID, fmt.Sprintf("cp/%s/outbound/%s/", childID, parentID))

	const reqID = "fixed-id-001"
	first, err := cli.Submit(ctx, Request{
		RequestID: reqID,
		Kind:      "counter",
		Body:      json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	// Re-issue the exact same request_id. The handler must not run
	// a second time; the Client should get the cached response.
	second, err := cli.Submit(ctx, Request{
		RequestID: reqID,
		Kind:      "counter",
		Body:      json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("second Submit: %v", err)
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("handler ran %d times, want 1 (idempotency cache miss?)", got)
	}
	if string(first.Body) != string(second.Body) {
		t.Fatalf("cached response differs: first=%q second=%q", first.Body, second.Body)
	}
}

func TestRoundtrip_MultiParent(t *testing.T) {
	bkt := newFakeBucket()
	const childID = "east"
	parents := []string{"global-1", "global-2", "global-3"}

	var seen sync.Map // parent -> RequestID
	srv := newTestServer(t, bkt, childID, "tag",
		func(ctx context.Context, req Request) Response {
			var in struct {
				Parent string `json:"parent"`
			}
			_ = json.Unmarshal(req.Body, &in)
			seen.Store(in.Parent, req.RequestID)
			return Response{HTTPStatus: 200, Body: req.Body}
		})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)

	var wg sync.WaitGroup
	for _, p := range parents {
		wg.Add(1)
		go func(parentID string) {
			defer wg.Done()
			cli := newTestClient(t, bkt, parentID,
				fmt.Sprintf("cp/%s/outbound/%s/", childID, parentID))
			body, _ := json.Marshal(map[string]string{"parent": parentID})
			if _, err := cli.Submit(ctx, Request{Kind: "tag", Body: body}); err != nil {
				t.Errorf("%s Submit: %v", parentID, err)
			}
		}(p)
	}
	wg.Wait()

	for _, p := range parents {
		if _, ok := seen.Load(p); !ok {
			t.Errorf("server did not observe directive from parent %q", p)
		}
	}
}

func TestRoundtrip_TimeoutWhenHandlerNeverResponds(t *testing.T) {
	bkt := newFakeBucket()
	const parentID, childID = "global", "east"

	// No server registered → req object lands in the bucket but no
	// resp ever appears. The Client must surface a timeout error.
	cli := newTestClient(t, bkt, parentID, fmt.Sprintf("cp/%s/outbound/%s/", childID, parentID))
	cli.timeout = 250 * time.Millisecond

	ctx := context.Background()
	start := time.Now()
	_, err := cli.Submit(ctx, Request{Kind: "ghost", Body: json.RawMessage(`{}`)})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error %q does not look like a timeout", err)
	}
	if elapsed < cli.timeout || elapsed > cli.timeout*4 {
		t.Errorf("Submit returned in %s; expected ~%s", elapsed, cli.timeout)
	}
}

func TestRoundtrip_UnregisteredKindReturns501(t *testing.T) {
	bkt := newFakeBucket()
	const parentID, childID = "global", "east"

	srv := newTestServer(t, bkt, childID, "known",
		func(ctx context.Context, req Request) Response {
			return Response{HTTPStatus: 200}
		})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)

	cli := newTestClient(t, bkt, parentID, fmt.Sprintf("cp/%s/outbound/%s/", childID, parentID))
	resp, err := cli.Submit(ctx, Request{Kind: "unknown", Body: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.HTTPStatus != 501 {
		t.Fatalf("HTTPStatus = %d, want 501", resp.HTTPStatus)
	}
	if resp.Status != "error" {
		t.Fatalf("Status = %q, want error", resp.Status)
	}
	if !strings.Contains(resp.Error, "no handler registered") {
		t.Fatalf("Error = %q, expected to mention missing handler", resp.Error)
	}
}

func TestRoundtrip_MalformedKeyIsSkipped(t *testing.T) {
	bkt := newFakeBucket()
	const childID = "east"

	srv := newTestServer(t, bkt, childID, "ok",
		func(ctx context.Context, req Request) Response {
			return Response{HTTPStatus: 200, Body: req.Body}
		})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)

	// Drop a valid-shaped req object.
	good := Request{
		RequestID: "good-1",
		Kind:      "ok",
		Body:      json.RawMessage(`{"x":1}`),
		IssuedAt:  time.Now().UTC(),
	}
	goodBody, _ := json.Marshal(good)
	_ = bkt.Put(ctx, "cp/global/outbound/east/req/good-1.json", goodBody, "application/json")

	// And a couple of malformed objects under the same prefix tree.
	_ = bkt.Put(ctx, "cp/global/outbound/east/req/not-json.txt",
		[]byte("garbage"), "text/plain")
	_ = bkt.Put(ctx, "cp/global/outbound/east/req/bad.json",
		[]byte("not-json"), "application/json")
	_ = bkt.Put(ctx, "cp/global/outbound/east/req/extra/nested.json",
		[]byte(`{}`), "application/json")

	// Wait for the response that proves the server stayed alive
	// despite the malformed neighbours.
	respKey := "cp/east/outbound/global/resp/good-1.json"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bkt.hasKey(respKey) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected response object %q to materialize despite malformed siblings", respKey)
}
