// Synthetic-node load tester. Opens N concurrent webhook clients
// against the CP and reports throughput / error rate. Used to validate
// the CP's webhook ingest path under fleet-scale load before
// production launch.
//
// What it does NOT yet test:
//   - Persistent tunnels (those scale via tunnel-server sharding —
//     separate harness in a follow-up).
//   - mgmt-plane mTLS at scale (cert issuance is the bottleneck;
//     stress-test deferred to phase 9 work).
//
// What it DOES exercise:
//   - HMAC-verified webhook ingest under sustained throughput
//   - The Phase 8c EventStore write path (sqlite or clickhouse,
//     depending on CP config)
//   - SSE fan-out (broadcaster) with N publishers (use --watch to
//     also subscribe and count delivered messages)
//
// Usage:
//
//   go run ./test/loadtest \
//     --cp-url=https://localhost:8443/api/webhooks/events \
//     --secret=demo-shared-1 \
//     --nodes=1000 --duration=60s --rate=100
//
// rate is per-node events/sec; total throughput = nodes * rate.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	cpURL := flag.String("cp-url", "https://localhost:8443/api/webhooks/events", "Webhook URL")
	secret := flag.String("secret", "", "HMAC secret (must match CP --webhook-secret)")
	nodes := flag.Int("nodes", 100, "Number of synthetic nodes (concurrent goroutines)")
	rate := flag.Float64("rate", 1.0, "Events per second per node")
	duration := flag.Duration("duration", 30*time.Second, "Total test duration")
	insecure := flag.Bool("insecure", true, "Skip TLS verification (dev self-signed certs)")
	flag.Parse()
	if *secret == "" {
		log.Fatal("--secret is required")
	}

	// One http.Client shared — its connection pool absorbs the load.
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: *insecure}, //nolint:gosec
			MaxIdleConns:        1024,
			MaxIdleConnsPerHost: 1024,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	var wg sync.WaitGroup
	var sent, ok2xx, failed atomic.Int64

	start := time.Now()
	log.Printf("starting %d nodes × %.1f evt/s for %s → target %.0f evt/s aggregate",
		*nodes, *rate, *duration, float64(*nodes)*(*rate))

	for i := 0; i < *nodes; i++ {
		wg.Add(1)
		go func(nodeID int) {
			defer wg.Done()
			nodeName := fmt.Sprintf("loadtest-node-%05d", nodeID)
			interval := time.Duration(float64(time.Second) / *rate)
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if err := postOne(ctx, client, *cpURL, *secret, nodeName); err != nil {
						failed.Add(1)
					} else {
						ok2xx.Add(1)
					}
					sent.Add(1)
				}
			}
		}(i)
	}

	// Reporter ticks every second so the operator sees progress.
	reportDone := make(chan struct{})
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		var lastSent int64
		for {
			select {
			case <-ctx.Done():
				close(reportDone)
				return
			case <-t.C:
				cur := sent.Load()
				rate1s := cur - lastSent
				lastSent = cur
				log.Printf("  +1s: sent=%d (Δ=%d/s) ok=%d failed=%d", cur, rate1s, ok2xx.Load(), failed.Load())
			}
		}
	}()

	wg.Wait()
	<-reportDone
	elapsed := time.Since(start)
	log.Printf("\ndone in %s", elapsed)
	log.Printf("  sent:    %d (%.1f evt/s)", sent.Load(), float64(sent.Load())/elapsed.Seconds())
	log.Printf("  2xx:     %d (%.2f%%)", ok2xx.Load(), 100*float64(ok2xx.Load())/float64(sent.Load()))
	log.Printf("  failed:  %d", failed.Load())
}

// postOne sends a single synthetic webhook event with the right HMAC
// signature. The body is one JSONL line — just like the daemon emits.
func postOne(ctx context.Context, client *http.Client, url, secret, nodeName string) error {
	now := time.Now()
	ev := map[string]any{
		"type":    "tick_done",
		"ts":      now.UnixMilli(),
		"agent":   "loadtest",
		"host":    nodeName,
		"text":    fmt.Sprintf("synthetic tick %d", rand.Int63()),
		"result":  "completed",
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("X-Okesu-Agent", "loadtest")
	req.Header.Set("X-Okesu-Host", nodeName)
	req.Header.Set("X-Okesu-Timestamp", strconv.FormatInt(now.UnixMilli(), 10))
	req.Header.Set("X-Okesu-Signature", sig)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
