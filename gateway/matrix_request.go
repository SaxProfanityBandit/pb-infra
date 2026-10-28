package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var matrixLocalpartRE = regexp.MustCompile(`^[a-z0-9._=\-/]+$`)

func matrixServerName() string {
	return envOr("MATRIX_SERVER_NAME", "chat.sax.lgbt")
}

func joinPublicBase() string {
	return strings.TrimRight(envOr("MATRIX_JOIN_PUBLIC_URL", "https://chat.sax.lgbt/join"), "/")
}

func homeserverPublicURL() string {
	return strings.TrimRight(envOr("MATRIX_HOMESERVER_URL", "https://chat.sax.lgbt"), "/")
}

func validateMatrixLocalpart(s string) error {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return fmt.Errorf("username required")
	}
	if utf8.RuneCountInString(s) > 255 || len(s) < 2 {
		return fmt.Errorf("username length invalid")
	}
	if !matrixLocalpartRE.MatchString(s) {
		return fmt.Errorf("username: lowercase letters, numbers, . _ = - / only")
	}
	return nil
}

type matrixRequestConfig struct {
	mailTo      string
	mailFrom    string
	smtpHost    string
	smtpUser    string
	smtpPass    string
	hookshotURL string
	signSecret  string
	approveBase string
	serverName  string
}

func loadMatrixRequestConfig(signSecret string, ma *matrixApproveConfig) *matrixRequestConfig {
	if ma == nil || strings.TrimSpace(os.Getenv("CONTACT_SMTP_USER")) == "" {
		return nil
	}
	return &matrixRequestConfig{
		mailTo:     envOr("CONTACT_MAIL_TO", "me@sax.lgbt"),
		mailFrom:   envOr("CONTACT_MAIL_FROM", "me@sax.lgbt"),
		smtpHost:   envOr("CONTACT_SMTP_HOST", "stalwart-mailserver.mailserver.svc.cluster.local:587"),
		smtpUser:   os.Getenv("CONTACT_SMTP_USER"),
		smtpPass:   os.Getenv("CONTACT_SMTP_PASSWORD"),
		hookshotURL: strings.TrimSpace(firstNonEmpty(
			os.Getenv("HOOKSHOT_WEBHOOK_INTERNAL_URL"),
			os.Getenv("HOOKSHOT_WEBHOOK_URL"),
		)),
		signSecret:  signSecret,
		approveBase: ma.publicURL,
		serverName:  matrixServerName(),
	}
}

func handleMatrixRequest(cfg *matrixRequestConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil {
			http.Error(w, "Not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireJoinGate(w, r, cfg.signSecret) {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad form", http.StatusBadRequest)
			return
		}
		if hp := strings.TrimSpace(r.FormValue("_hp")); hp != "" {
			http.Redirect(w, r, joinPublicBase(), http.StatusSeeOther)
			return
		}
		localpart := strings.TrimSpace(strings.ToLower(r.FormValue("username")))
		email := strings.TrimSpace(r.FormValue("email"))
		display := strings.TrimSpace(r.FormValue("display_name"))
		if err := validateMatrixLocalpart(localpart); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if email != "" {
			if _, err := mail.ParseAddress(email); err != nil {
				http.Error(w, "Invalid email", http.StatusBadRequest)
				return
			}
		}
		rid, err := newRequestID()
		if err != nil {
			http.Error(w, "Could not create request", http.StatusInternalServerError)
			return
		}
		putPendingRequest(rid, &pendingRequest{
			Localpart: localpart,
			Email:     email,
			Display:   display,
			CreatedAt: time.Now(),
		})
		mxid := "@" + localpart + ":" + cfg.serverName
		approveURL := buildMatrixApproveURL(cfg.signSecret, cfg.approveBase, email, display, localpart, rid)

		subject := fmt.Sprintf("[chat.sax.lgbt] Access request %s", mxid)
		var body strings.Builder
		fmt.Fprintf(&body, "Matrix access request\n\n")
		fmt.Fprintf(&body, "Requested ID: %s\n", mxid)
		if email != "" {
			fmt.Fprintf(&body, "Email: %s\n", email)
		} else {
			fmt.Fprintf(&body, "Email: (not provided)\n")
		}
		if display != "" {
			fmt.Fprintf(&body, "Display name: %s\n", display)
		}
		fmt.Fprintf(&body, "\nApprove URL:\n%s\n", approveURL)

		if err := sendSTARTTLS(cfg.smtpHost, cfg.smtpUser, cfg.smtpPass, cfg.mailFrom, []string{cfg.mailTo}, plainEmail(cfg.mailFrom, cfg.mailTo, subject, body.String())); err != nil {
			log.Printf("matrix request smtp: %v", err)
			http.Error(w, "Could not send request", http.StatusBadGateway)
			return
		}
		if err := notifyMatrixRequestHookshot(r.Context(), cfg, mxid, email, display, approveURL); err != nil {
			log.Printf("matrix request hookshot: %v", err)
		}
		wsig := waitSig(cfg.signSecret, rid)
		q := url.Values{}
		q.Set("rid", rid)
		q.Set("wsig", wsig)
		if email != "" {
			q.Set("mail", "1")
		}
		joinURL := joinPublicBase() + "?" + q.Encode()
		if acceptsJSON(r) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			out := map[string]string{"status": "pending", "rid": rid, "wsig": wsig, "join_url": joinURL}
			if email != "" {
				out["mail"] = "1"
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		http.Redirect(w, r, joinURL, http.StatusSeeOther)
	}
}

func acceptsJSON(r *http.Request) bool {
	if r.Header.Get("X-Requested-With") == "fetch" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json")
}

func handleMatrixRequestStatus(cfg *matrixRequestConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil {
			http.Error(w, "Not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rid := strings.TrimSpace(r.URL.Query().Get("rid"))
		wsig := strings.TrimSpace(r.URL.Query().Get("wsig"))
		if rid == "" || wsig == "" || !validWaitSig(cfg.signSecret, rid, wsig) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		pr, ok := getPendingRequest(rid)
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "expired", "rid": rid})
			return
		}
		if !pr.Ready || pr.Token == "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending", "rid": rid})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":     "ready",
			"rid":        rid,
			"token":      pr.Token,
			"user":       pr.Localpart,
			"server":     cfg.serverName,
			"homeserver": homeserverPublicURL(),
		})
	}
}

func notifyMatrixRequestHookshot(ctx context.Context, cfg *matrixRequestConfig, mxid, email, display, approveURL string) error {
	if cfg.hookshotURL == "" {
		return nil
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Matrix access request (chat.sax.lgbt)\n\n")
	fmt.Fprintf(&text, "Requested ID: %s\n", mxid)
	if email != "" {
		fmt.Fprintf(&text, "Email: %s\n", email)
	} else {
		fmt.Fprintf(&text, "Email: (not provided)\n")
	}
	if display != "" {
		fmt.Fprintf(&text, "Display name: %s\n", display)
	}
	payload := map[string]string{
		"text":        text.String(),
		"username":    "chat.sax.lgbt",
		"approve_url": approveURL,
	}
	return postHookshotJSON(ctx, cfg.hookshotURL, payload)
}
