package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxContactName    = 200
	maxContactEmail   = 254
	maxContactMessage = 8000
)

type contactConfig struct {
	smtpHost    string
	smtpUser    string
	smtpPass    string
	mailTo      string
	mailFrom    string
	hookshotURL string
	publicURL   string
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func loadContactConfig() *contactConfig {
	if strings.TrimSpace(os.Getenv("CONTACT_SMTP_USER")) == "" {
		return nil
	}
	return &contactConfig{
		smtpHost: envOr("CONTACT_SMTP_HOST", "stalwart-mailserver.mailserver.svc.cluster.local:587"),
		smtpUser:   os.Getenv("CONTACT_SMTP_USER"),
		smtpPass:   os.Getenv("CONTACT_SMTP_PASSWORD"),
		mailTo:     envOr("CONTACT_MAIL_TO", "me@sax.lgbt"),
		mailFrom:   envOr("CONTACT_MAIL_FROM", "me@sax.lgbt"),
		hookshotURL: strings.TrimSpace(firstNonEmpty(
			os.Getenv("HOOKSHOT_WEBHOOK_INTERNAL_URL"),
			os.Getenv("HOOKSHOT_WEBHOOK_URL"),
		)),
		publicURL: strings.TrimRight(envOr("SITE_PUBLIC_URL", "https://rickardsahlgren.name"), "/"),
	}
}

func handleContact(cfg *contactConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil {
			http.Error(w, "Contact form is not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad form", http.StatusBadRequest)
			return
		}
		if hp := strings.TrimSpace(r.FormValue("_hp")); hp != "" {
			http.Redirect(w, r, cfg.publicURL+"/?contact=sent", http.StatusSeeOther)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		email := strings.TrimSpace(r.FormValue("email"))
		message := strings.TrimSpace(r.FormValue("message"))
		if name == "" || message == "" {
			http.Error(w, "Name and message are required", http.StatusBadRequest)
			return
		}
		if utf8.RuneCountInString(name) > maxContactName ||
			utf8.RuneCountInString(message) > maxContactMessage ||
			utf8.RuneCountInString(email) > maxContactEmail {
			http.Error(w, "Input too long", http.StatusBadRequest)
			return
		}
		if email != "" {
			if _, err := mail.ParseAddress(email); err != nil {
				http.Error(w, "Invalid email", http.StatusBadRequest)
				return
			}
		}
		subject := fmt.Sprintf("[rickardsahlgren.name] Contact from %s", name)
		var body strings.Builder
		fmt.Fprintf(&body, "Contact form on %s\n\n", cfg.publicURL)
		fmt.Fprintf(&body, "Name: %s\n", name)
		if email != "" {
			fmt.Fprintf(&body, "Email: %s\n", email)
		}
		fmt.Fprintf(&body, "\n%s\n", message)

		if err := sendContactSMTP(cfg, subject, body.String(), email); err != nil {
			log.Printf("contact smtp: %v", err)
			http.Error(w, "Could not send message", http.StatusBadGateway)
			return
		}
		if err := notifyHookshot(r.Context(), cfg, name, email, message); err != nil {
			log.Printf("contact hookshot: %v", err)
		}
		http.Redirect(w, r, cfg.publicURL+"/?contact=sent", http.StatusSeeOther)
	}
}

func sendContactSMTP(cfg *contactConfig, subject, body, replyTo string) error {
	from := cfg.mailFrom
	to := cfg.mailTo
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	if replyTo != "" {
		fmt.Fprintf(&msg, "Reply-To: %s\r\n", replyTo)
	}
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)
	return sendSTARTTLS(cfg.smtpHost, cfg.smtpUser, cfg.smtpPass, from, []string{to}, msg.Bytes())
}

func smtpTLSConfig(host string) *tls.Config {
	serverName := strings.TrimSpace(os.Getenv("CONTACT_SMTP_TLS_SERVER_NAME"))
	if serverName == "" {
		serverName = host
	}
	cfg := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CONTACT_SMTP_TLS_INSECURE"))) {
	case "1", "true", "yes":
		cfg.InsecureSkipVerify = true
	}
	return cfg
}

func sendSTARTTLS(addr, user, pass, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(smtpTLSConfig(host)); err != nil {
			return err
		}
	}
	if user != "" {
		auth := smtp.PlainAuth("", user, pass, host)
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func notifyHookshot(ctx context.Context, cfg *contactConfig, name, email, message string) error {
	if cfg.hookshotURL == "" {
		return nil
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Contact form (%s)\n\n", cfg.publicURL)
	fmt.Fprintf(&text, "Name: %s\n", name)
	if email != "" {
		fmt.Fprintf(&text, "Email: %s\n", email)
	}
	fmt.Fprintf(&text, "\n%s", message)
	payload := map[string]string{
		"text":     text.String(),
		"username": "rickardsahlgren.name",
	}
	return postHookshotJSON(ctx, cfg.hookshotURL, payload)
}

func postHookshotJSON(ctx context.Context, hookshotURL string, payload map[string]string) error {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hookshotURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hookshot HTTP %s", resp.Status)
	}
	return nil
}
