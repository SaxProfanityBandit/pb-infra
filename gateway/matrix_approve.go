package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const matrixApproveTTL = 7 * 24 * time.Hour

type matrixApproveConfig struct {
	synapseURL      string
	adminToken      string
	publicURL       string
	smtpHost        string
	smtpUser        string
	smtpPass        string
	mailFrom        string
	daysValid       int
	modLogHookshot  string
}

func matrixApprovePublicBase() string {
	if u := strings.TrimSpace(os.Getenv("MATRIX_APPROVE_PUBLIC_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "https://approve.sax.lgbt"
}

func loadMatrixApproveConfig(_ string) *matrixApproveConfig {
	publicURL := matrixApprovePublicBase()
	token := strings.TrimSpace(os.Getenv("SYNAPSE_ADMIN_ACCESS_TOKEN"))
	if token == "" {
		return nil
	}
	days := 14
	if d := strings.TrimSpace(os.Getenv("MATRIX_REG_TOKEN_DAYS_VALID")); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			days = n
		}
	}
	cfg := &matrixApproveConfig{
		synapseURL: strings.TrimRight(envOr("SYNAPSE_URL", "http://synapse-matrix-synapse.matrix.svc.cluster.local:8008"), "/"),
		adminToken: token,
		publicURL:  strings.TrimRight(publicURL, "/"),
		daysValid:  days,
		modLogHookshot: strings.TrimSpace(firstNonEmpty(
			os.Getenv("MOD_LOG_HOOKSHOT_URL"),
			os.Getenv("HOOKSHOT_WEBHOOK_INTERNAL_URL"),
			os.Getenv("HOOKSHOT_WEBHOOK_URL"),
		)),
	}
	if u := strings.TrimSpace(os.Getenv("CONTACT_SMTP_USER")); u != "" {
		cfg.smtpHost = envOr("CONTACT_SMTP_HOST", "stalwart-mailserver.mailserver.svc.cluster.local:587")
		cfg.smtpUser = u
		cfg.smtpPass = os.Getenv("CONTACT_SMTP_PASSWORD")
		cfg.mailFrom = envOr("CONTACT_MAIL_FROM", "me@sax.lgbt")
	}
	return cfg
}

func matrixApprovePayload(expStr, email, name, localpart, requestID string) string {
	return "matrix-approve:" + expStr + "|" + strings.ToLower(strings.TrimSpace(email)) + "|" + strings.TrimSpace(name) + "|" + strings.ToLower(strings.TrimSpace(localpart)) + "|" + strings.TrimSpace(requestID)
}

func validPayloadSig(secret, payload, sig string) bool {
	expected := sign(secret, payload)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) == 1
}

func buildMatrixApproveURL(secret, publicURL, email, name, localpart, requestID string) string {
	exp := time.Now().Add(matrixApproveTTL).Unix()
	expStr := strconv.FormatInt(exp, 10)
	payload := matrixApprovePayload(expStr, email, name, localpart, requestID)
	sig := sign(secret, payload)
	q := url.Values{}
	q.Set("exp", expStr)
	q.Set("sig", sig)
	if email != "" {
		q.Set("email", email)
	}
	if name != "" {
		q.Set("name", name)
	}
	if localpart != "" {
		q.Set("user", localpart)
	}
	if requestID != "" {
		q.Set("rid", requestID)
	}
	return publicURL + "/matrix/approve?" + q.Encode()
}

func buildJoinURL(token, localpart string) string {
	base := joinPublicBase()
	q := url.Values{}
	q.Set("token", token)
	if localpart != "" {
		q.Set("user", localpart)
	}
	return base + "?" + q.Encode()
}

type approveParams struct {
	email     string
	name      string
	localpart string
	server    string
	requestID string
}

func validateApproveRequest(r *http.Request, secret string) (approveParams, error) {
	expStr := strings.TrimSpace(r.URL.Query().Get("exp"))
	sig := strings.TrimSpace(r.URL.Query().Get("sig"))
	if expStr == "" || sig == "" {
		return approveParams{}, fmt.Errorf("missing exp or sig")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return approveParams{}, fmt.Errorf("bad exp")
	}
	if time.Now().Unix() > exp {
		return approveParams{}, fmt.Errorf("expired")
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	localpart := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("user")))
	requestID := strings.TrimSpace(r.URL.Query().Get("rid"))
	payload := matrixApprovePayload(expStr, email, name, localpart, requestID)
	if !validPayloadSig(secret, payload, sig) {
		return approveParams{}, fmt.Errorf("bad sig")
	}
	return approveParams{
		email: email, name: name, localpart: localpart, server: matrixServerName(), requestID: requestID,
	}, nil
}

type regTokenResponse struct {
	Token string `json:"token"`
}

func synapseNewRegistrationToken(ctx context.Context, cfg *matrixApproveConfig) (string, error) {
	body := map[string]interface{}{"uses_allowed": 1}
	if cfg.daysValid > 0 {
		body["expiry_time"] = time.Now().Add(time.Duration(cfg.daysValid) * 24 * time.Hour).UnixMilli()
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.synapseURL+"/_synapse/admin/v1/registration_tokens/new", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("synapse %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out regTokenResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("empty token from synapse")
	}
	return out.Token, nil
}

func notifyMatrixApprovedModLog(ctx context.Context, cfg *matrixApproveConfig, p approveParams, emailed bool, mailErr error) error {
	if cfg.modLogHookshot == "" {
		return nil
	}
	server := matrixServerName()
	var text strings.Builder
	fmt.Fprintf(&text, "Registration approved (staff log)\n\n")
	if p.localpart != "" {
		fmt.Fprintf(&text, "Matrix ID: @%s:%s\n", p.localpart, server)
	} else {
		fmt.Fprintf(&text, "Matrix ID: (not specified on request)\n")
	}
	if p.name != "" {
		fmt.Fprintf(&text, "Display name on request: %s\n", p.name)
	}
	if p.email != "" {
		fmt.Fprintf(&text, "Email: %s\n", p.email)
		if emailed && mailErr == nil {
			text.WriteString("Token delivery: emailed\n")
		} else if emailed && mailErr != nil {
			text.WriteString("Token delivery: email failed (use approve page backup link)\n")
		} else {
			text.WriteString("Token delivery: join tab / manual link (email skipped)\n")
		}
	} else {
		text.WriteString("Email: (not provided)\n")
		text.WriteString("Token delivery: join tab / manual link\n")
	}
	payload := map[string]string{
		"text":     text.String(),
		"username": "chat.sax.lgbt",
	}
	return postHookshotJSON(ctx, cfg.modLogHookshot, payload)
}

func sendInviteEmail(cfg *matrixApproveConfig, to, token, joinURL, localpart string) error {
	if cfg.smtpUser == "" || to == "" {
		return fmt.Errorf("smtp or recipient not configured")
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return err
	}
	hs := homeserverPublicURL()
	subject := "chat.sax.lgbt registration approved"
	server := matrixServerName()
	mxid := localpart
	if mxid != "" {
		mxid = "@" + localpart + ":" + server
	}
	bodyText := ""
	if tpl, ok := loadRegistrationTemplate("approval-email.txt"); ok {
		bodyText = renderTemplate(tpl, map[string]string{
			"localpart":       localpart,
			"server_name":     server,
			"mxid":            mxid,
			"token":           token,
			"join_url":        joinURL,
			"homeserver_url":  hs,
		})
	} else {
		var body strings.Builder
		fmt.Fprintf(&body, "chat.sax.lgbt registration approved.\n\n")
		if localpart != "" {
			fmt.Fprintf(&body, "Username: @%s:%s\n\n", localpart, server)
		}
		fmt.Fprintf(&body, "Registration token (one use):\n\n  %s\n\n", token)
		fmt.Fprintf(&body, "Join page (token + client steps):\n\n  %s\n\n", joinURL)
		fmt.Fprintf(&body, "Homeserver URL: %s\n\n", hs)
		bodyText = body.String()
	}
	return sendSTARTTLS(cfg.smtpHost, cfg.smtpUser, cfg.smtpPass, cfg.mailFrom, []string{to}, plainEmail(cfg.mailFrom, to, subject, bodyText))
}

func plainEmail(from, to, subject, body string) []byte {
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	msg.WriteString(body)
	return msg.Bytes()
}

func escHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func handleMatrixApprove(secret string, cfg *matrixApproveConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil {
			http.Error(w, "Not configured", http.StatusServiceUnavailable)
			return
		}
		params, err := validateApproveRequest(r, secret)
		if err != nil {
			http.Error(w, "Invalid or expired link", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")

		if r.Method == http.MethodGet {
			serveApproveConfirmPage(w, r, params)
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
		token, err := synapseNewRegistrationToken(r.Context(), cfg)
		if err != nil {
			log.Printf("matrix approve synapse: %v", err)
			http.Error(w, "Could not create token", http.StatusBadGateway)
			return
		}
		sendMail := params.email != "" && r.FormValue("skip_email") != "1"
		var mailErr error
		joinURL := buildJoinURL(token, params.localpart)
		fulfillPendingRequest(params.requestID, token, params.localpart)
		if sendMail {
			mailErr = sendInviteEmail(cfg, params.email, token, joinURL, params.localpart)
			if mailErr != nil {
				log.Printf("matrix approve invite mail: %v", mailErr)
			}
		}
		if err := notifyMatrixApprovedModLog(r.Context(), cfg, params, sendMail, mailErr); err != nil {
			log.Printf("matrix approve mod-log hookshot: %v", err)
		}
		serveApproveResult(w, params, joinURL, sendMail, mailErr)
	}
}

func serveApproveConfirmPage(w http.ResponseWriter, r *http.Request, p approveParams) {
	email := escHTML(p.email)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Approve Matrix registration</title>
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:2rem auto;padding:0 1rem;line-height:1.5}button{font-size:1.1rem;padding:.6rem 1.2rem}label{display:block;margin:1rem 0}</style></head><body>`)
	b.WriteString(`<h1>Approve registration request</h1>`)
	if p.localpart != "" {
		fmt.Fprintf(&b, `<p><strong>Matrix ID:</strong> @%s:%s</p>`, escHTML(p.localpart), escHTML(p.server))
	}
	if p.name != "" {
		fmt.Fprintf(&b, `<p><strong>Name:</strong> %s</p>`, escHTML(p.name))
	}
	if p.email != "" {
		fmt.Fprintf(&b, `<p><strong>Email:</strong> %s</p>`, email)
	}
	b.WriteString(`<p>Issues a one-use token and a join link on chat.sax.lgbt.</p><form method="POST" action="`)
	b.WriteString(escHTML(r.URL.RequestURI()))
	b.WriteString(`">`)
	if p.email != "" {
		fmt.Fprintf(&b, `<p class="muted">Token and link go to %s unless you tick skip below.</p>`, email)
		fmt.Fprintf(&b, `<label><input type="checkbox" name="skip_email" value="1"> Skip email (they need the join tab open)</label>`)
	}
	b.WriteString(`<p><button type="submit">Approve</button></p></form></body></html>`)
	_, _ = w.Write([]byte(b.String()))
}

func serveApproveResult(w http.ResponseWriter, p approveParams, joinURL string, sent bool, mailErr error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Request approved</title>
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:2rem auto;padding:0 1rem;line-height:1.5}.ok{color:#0a6}.mono{font-family:monospace;word-break:break-all;background:#f4f4f4;padding:.5rem;font-size:.85rem}details{margin-top:1.25rem}button{margin-top:.5rem;font-size:.95rem;padding:.45rem .85rem}</style></head><body>`)
	b.WriteString(`<h1>Request approved</h1>`)
	if p.localpart != "" {
		fmt.Fprintf(&b, `<p class="ok"><strong>@%s:%s</strong> can finish registration.</p>`, escHTML(p.localpart), escHTML(p.server))
	}
	b.WriteString(`<p>You do not need to open the join link; this is just your confirmation.</p>`)
	if p.email != "" {
		if sent && mailErr == nil {
			fmt.Fprintf(&b, `<p>Token and join link sent to <strong>%s</strong>.</p>`, escHTML(p.email))
		} else if sent && mailErr != nil {
			b.WriteString(`<p><strong>Email failed.</strong> Paste the backup link below.</p>`)
		}
	} else {
		b.WriteString(`<p>No email on the request. They need the join tab open, or you send the link below.</p>`)
	}
	b.WriteString(`<p class="muted">If they left chat.sax.lgbt/join open on "Awaiting approval", it should flip to the token on its own.</p>`)
	b.WriteString(`<details><summary>Backup join link</summary><p class="mono" id="join">`)
	b.WriteString(escHTML(joinURL))
	b.WriteString(`</p><button type="button" onclick="navigator.clipboard.writeText(document.getElementById('join').innerText)">Copy link</button></details>`)
	b.WriteString(`</body></html>`)
	_, _ = w.Write([]byte(b.String()))
}
