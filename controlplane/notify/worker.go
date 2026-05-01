package notify

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// Subscriber is the publish/subscribe surface the worker needs from the
// CP's broadcaster — an open-ended stream of raw JSONL event lines.
type Subscriber interface {
	Subscribe() (<-chan []byte, func())
}

// Sender delivers one finding to one channel-config blob.
type Sender interface {
	Send(ctx context.Context, channelConfig []byte, f *Finding) error
}

// Worker subscribes to incoming event lines, filters down to findings,
// matches them against enabled rules, and dispatches deliveries with retry.
type Worker struct {
	Store      *db.Store
	Subscriber Subscriber

	// MaxRetries per delivery (0 = no retry, 1 attempt). Default 3.
	MaxRetries int

	// HTTP for slack + webhook senders. Reused so connection pooling kicks in.
	HTTP *http.Client

	// Senders is overridable for tests. Defaults to {slack, email, webhook}.
	Senders map[string]Sender
}

// Run blocks until ctx is cancelled. Spawn it once at server startup.
func (w *Worker) Run(ctx context.Context) {
	if w.MaxRetries <= 0 {
		w.MaxRetries = 3
	}
	if w.HTTP == nil {
		w.HTTP = &http.Client{Timeout: 12 * time.Second}
	}
	if w.Senders == nil {
		w.Senders = map[string]Sender{
			db.ChannelTypeSlack:   &SlackSender{HTTP: w.HTTP},
			db.ChannelTypeEmail:   &EmailSender{},
			db.ChannelTypeWebhook: &WebhookSender{HTTP: w.HTTP},
		}
	}

	ch, cancel := w.Subscriber.Subscribe()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			w.handleLine(ctx, line)
		}
	}
}

func (w *Worker) handleLine(ctx context.Context, line []byte) {
	f := ParseFinding(line)
	if f == nil {
		return
	}
	rules, err := w.Store.EnabledRulesWithChannels()
	if err != nil {
		log.Printf("notify: list rules: %v", err)
		return
	}
	for _, er := range rules {
		if !w.matches(er.Rule, f) {
			continue
		}
		// Capture the finding/channel so the goroutine doesn't share loop state.
		rule := er.Rule
		channel := er.Channel
		go w.deliver(ctx, rule, channel, *f)
	}
}

func (w *Worker) matches(rule db.NotificationRule, f *Finding) bool {
	if !SeverityAtLeast(f.Severity, rule.MinSeverity) {
		return false
	}
	if rule.AgentSubstring.Valid && rule.AgentSubstring.String != "" {
		if !strings.Contains(f.Agent, rule.AgentSubstring.String) {
			return false
		}
	}
	if rule.HostSubstring.Valid && rule.HostSubstring.String != "" {
		if !strings.Contains(f.Host, rule.HostSubstring.String) {
			return false
		}
	}
	// Phase 22.9 — K8s-style label selector against the finding's
	// host node labels. Bad selectors fail-closed: a malformed rule
	// shouldn't accidentally route every finding to its channel
	// when the operator's intent was a tighter scope.
	if rule.HostSelector != "" {
		sel, err := db.ParseSelector(rule.HostSelector)
		if err != nil {
			return false
		}
		labels, err := w.Store.NodeLabelsForHost(f.Host)
		if err != nil {
			return false
		}
		if !sel.Matches(labels) {
			return false
		}
	}
	return true
}

// SendNow runs a one-shot delivery against a specific channel — used by
// the "test channel" admin button. No retry, no delivery row written.
func (w *Worker) SendNow(ctx context.Context, channel db.NotificationChannel, f Finding) error {
	if w.HTTP == nil {
		w.HTTP = &http.Client{Timeout: 12 * time.Second}
	}
	if w.Senders == nil {
		w.Senders = map[string]Sender{
			db.ChannelTypeSlack:   &SlackSender{HTTP: w.HTTP},
			db.ChannelTypeEmail:   &EmailSender{},
			db.ChannelTypeWebhook: &WebhookSender{HTTP: w.HTTP},
		}
	}
	sender, ok := w.Senders[channel.Type]
	if !ok {
		return fmt.Errorf("unknown channel type %q", channel.Type)
	}
	return sender.Send(ctx, []byte(channel.Config), &f)
}

func (w *Worker) deliver(ctx context.Context, rule db.NotificationRule, channel db.NotificationChannel, f Finding) {
	// Open a delivery row so the UI sees the attempt immediately.
	deliveryID, err := w.Store.CreateDelivery(rule.ID, channel.ID, f.ID, f.Severity, f.Title)
	if err != nil {
		log.Printf("notify: create delivery: %v", err)
		return
	}
	sender, ok := w.Senders[channel.Type]
	if !ok {
		_ = w.Store.FinishDelivery(deliveryID, "failed", fmt.Sprintf("unknown channel type %q", channel.Type))
		return
	}

	var lastErr error
	for attempt := 1; attempt <= w.MaxRetries; attempt++ {
		_ = w.Store.UpdateDeliveryAttempt(deliveryID, attempt)

		// Per-attempt timeout that respects the parent ctx.
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := sender.Send(attemptCtx, []byte(channel.Config), &f)
		cancel()
		if err == nil {
			_ = w.Store.FinishDelivery(deliveryID, "succeeded", "")
			return
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
		// Exponential back-off: 1s, 3s, 9s, capped.
		backoff := time.Duration(attempt*attempt) * time.Second
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
		}
	}
	msg := "no error captured"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	_ = w.Store.FinishDelivery(deliveryID, "failed", msg)
}

var _ = sync.Mutex{} // reserved for future delivery-rate-limiting
