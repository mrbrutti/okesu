package notify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// EmailConfig is the JSON shape persisted in notification_channels.config
// for type=email.
type EmailConfig struct {
	SMTPHost string   `json:"smtp_host"`
	SMTPPort int      `json:"smtp_port"`             // typically 587 or 465
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from"`                  // "alerts@example.com" or "Alerts <alerts@example.com>"
	To       []string `json:"to"`
	UseTLS   bool     `json:"tls"`                   // implicit TLS (port 465-style)
	UseSTART bool     `json:"starttls"`              // STARTTLS upgrade after plain connect
}

// EmailSender delivers a finding via SMTP. Supports plain, STARTTLS,
// and implicit-TLS connections; auth is optional.
type EmailSender struct{}

func (s *EmailSender) Send(ctx context.Context, raw []byte, f *Finding) error {
	var cfg EmailConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("invalid email config: %w", err)
	}
	if cfg.SMTPHost == "" || cfg.SMTPPort == 0 || cfg.From == "" || len(cfg.To) == 0 {
		return fmt.Errorf("email: smtp_host, smtp_port, from, and to are required")
	}

	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
	subject := fmt.Sprintf("[%s] %s", f.Severity, f.Title)

	body := buildEmailBody(f)
	msg := buildEmailMIME(cfg.From, cfg.To, subject, body)

	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	deadline, _ := ctx.Deadline()

	switch {
	case cfg.UseTLS:
		// Implicit TLS — the connection itself is TLS-wrapped from the start.
		tlsCfg := &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12}
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("dial tls: %w", err)
		}
		if !deadline.IsZero() {
			_ = conn.SetDeadline(deadline)
		}
		c, err := smtp.NewClient(conn, cfg.SMTPHost)
		if err != nil {
			return err
		}
		defer c.Quit()
		return sendVia(c, auth, cfg.From, cfg.To, msg)

	default:
		// Plain TCP, optionally followed by STARTTLS.
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
		if !deadline.IsZero() {
			_ = conn.SetDeadline(deadline)
		}
		c, err := smtp.NewClient(conn, cfg.SMTPHost)
		if err != nil {
			return err
		}
		defer c.Quit()
		if cfg.UseSTART {
			if err := c.StartTLS(&tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
		return sendVia(c, auth, cfg.From, cfg.To, msg)
	}
}

func sendVia(c *smtp.Client, auth smtp.Auth, from string, to []string, msg []byte) error {
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := c.Mail(extractEmail(from)); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, addr := range to {
		if err := c.Rcpt(extractEmail(addr)); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", addr, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return fmt.Errorf("write body: %w", err)
	}
	return wc.Close()
}

func buildEmailBody(f *Finding) string {
	var b strings.Builder
	b.WriteString("Severity:  ")
	b.WriteString(f.Severity)
	b.WriteString("\nAgent:     ")
	b.WriteString(f.Agent)
	b.WriteString("\nHost:      ")
	b.WriteString(f.Host)
	if f.Resource != "" {
		b.WriteString("\nResource:  ")
		b.WriteString(f.Resource)
	}
	b.WriteString("\nTime:      ")
	b.WriteString(time.UnixMilli(f.Ts).UTC().Format(time.RFC3339))
	b.WriteString("\n\n")
	b.WriteString(f.Title)
	b.WriteString("\n")
	if f.Evidence != "" {
		b.WriteString("\nEvidence:\n")
		b.WriteString(f.Evidence)
		b.WriteString("\n")
	}
	if f.Action != "" {
		b.WriteString("\nRecommended action:\n")
		b.WriteString(f.Action)
		b.WriteString("\n")
	}
	if f.DedupKey != "" {
		b.WriteString("\nDedup key: ")
		b.WriteString(f.DedupKey)
		b.WriteString("\n")
	}
	return b.String()
}

func buildEmailMIME(from string, to []string, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

// extractEmail strips an optional "Name <addr@domain>" wrapper.
func extractEmail(s string) string {
	if i := strings.LastIndexByte(s, '<'); i >= 0 {
		if j := strings.LastIndexByte(s, '>'); j > i {
			return strings.TrimSpace(s[i+1 : j])
		}
	}
	return strings.TrimSpace(s)
}
