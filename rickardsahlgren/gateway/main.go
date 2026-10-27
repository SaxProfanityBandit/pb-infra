package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	cookieName = "resume_gate"
	cookiePath = "/"
)

func main() {
	secret := os.Getenv("RESUME_LINK_KEY")
	if secret == "" {
		log.Fatal("RESUME_LINK_KEY is not set")
	}
	publicRoot := envOr("PUBLIC_ROOT", "/srv/public")
	privateCV := envOr("PRIVATE_CV_ROOT", "/srv/private/cv")
	baseURL := strings.TrimRight(envOr("SITE_PUBLIC_URL", "https://rickardsahlgren.name"), "/")
	addr := envOr("LISTEN", ":8080")
	adminUser := os.Getenv("ADMIN_USER")
	adminPass := os.Getenv("ADMIN_PASSWORD")

	mux := http.NewServeMux()
	mux.HandleFunc("/access", func(w http.ResponseWriter, r *http.Request) {
		handleAccess(w, r, secret)
	})
	cvFS := http.FileServer(http.Dir(privateCV))
	mux.Handle("/cv/", gateStatic(secret, http.StripPrefix("/cv/", cvFS), "/cv/"))

	if adminUser != "" && adminPass != "" {
		admin := http.NewServeMux()
		admin.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" && r.URL.Path != "" {
				http.NotFound(w, r)
				return
			}
			handleAdminUI(w, r, baseURL, secret)
		})
		admin.HandleFunc("/link", func(w http.ResponseWriter, r *http.Request) {
			handleAdminLink(w, r, baseURL, secret)
		})
		mux.Handle("/admin/", http.StripPrefix("/admin", basicAuth(adminUser, adminPass, admin)))
		mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusFound)
		})
		log.Print("admin UI enabled at /admin/")
	} else {
		log.Print("admin UI disabled (set ADMIN_USER and ADMIN_PASSWORD)")
	}

	mux.Handle("/", http.FileServer(http.Dir(publicRoot)))

	s := &http.Server{
		Addr:              addr,
		Handler:           withRobots(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on %s", addr)
	log.Fatal(s.ListenAndServe())
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func withRobots(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, nosnippet, noarchive")
		next.ServeHTTP(w, r)
	})
}

func basicAuth(user, pass string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="CV links", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func gateStatic(secret string, fs http.Handler, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		if err := authorizeRequest(r, secret); err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		clean := filepath.Clean(r.URL.Path)
		prefixClean := filepath.Clean(prefix)
		if clean != prefixClean && !strings.HasPrefix(clean, prefixClean+"/") {
			http.NotFound(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func handleAccess(w http.ResponseWriter, r *http.Request, secret string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	expStr := r.URL.Query().Get("exp")
	sig := r.URL.Query().Get("sig")
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || expStr == "" || sig == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if time.Now().Unix() > exp {
		http.Error(w, "Link expired", http.StatusForbidden)
		return
	}
	if !validSig(secret, expStr, sig) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	maxAge := exp - time.Now().Unix()
	if maxAge < 1 {
		http.Error(w, "Link expired", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    expStr + "." + sig,
		Path:     cookiePath,
		MaxAge:   int(maxAge),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/cv/", http.StatusFound)
}

func handleAdminLink(w http.ResponseWriter, r *http.Request, baseURL, secret string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	days := parseDays(r)
	link, exp, err := buildAccessLink(baseURL, secret, days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Header.Get("Accept") == "text/plain" || r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintln(w, link)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = fmt.Fprintf(w, `{"url":%q,"exp":%d,"days":%d}`+"\n", link, exp, days)
}

func handleAdminUI(w http.ResponseWriter, r *http.Request, baseURL, secret string) {
	days := 14
	var link string
	var exp int64
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil {
			days = parseDays(r)
			var err error
			link, exp, err = buildAccessLink(baseURL, secret, days)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	linkBlock := ""
	if link != "" {
		esc := html.EscapeString(link)
		expHint := html.EscapeString(time.Unix(exp, 0).UTC().Format("2006-01-02 15:04 UTC"))
		linkBlock = fmt.Sprintf(
			`<p><label for="out">Link</label></p><output id="out"><a href="%s">%s</a></output><p class="meta">Expires %s</p>`,
			esc, esc, expHint,
		)
	}
	fmt.Fprintf(w, adminPageHTML, days, linkBlock)
}

func parseDays(r *http.Request) int {
	raw := r.URL.Query().Get("days")
	if r.Method == http.MethodPost {
		raw = r.FormValue("days")
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 {
		days = 14
	}
	if days > 365 {
		days = 365
	}
	return days
}

func buildAccessLink(baseURL, secret string, days int) (string, int64, error) {
	exp := time.Now().Unix() + int64(days)*86400
	expStr := strconv.FormatInt(exp, 10)
	sig := sign(secret, expStr)
	return baseURL + "/access?exp=" + expStr + "&sig=" + sig, exp, nil
}

func authorizeRequest(r *http.Request, secret string) error {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return errors.New("no cookie")
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return errors.New("bad cookie")
	}
	expStr, sig := parts[0], parts[1]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return err
	}
	if time.Now().Unix() > exp {
		return errors.New("expired")
	}
	if !validSig(secret, expStr, sig) {
		return errors.New("bad sig")
	}
	return nil
}

func validSig(secret, expStr, sig string) bool {
	expected := sign(secret, expStr)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) == 1
}

func sign(secret, expStr string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(expStr))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

const adminPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="robots" content="noindex, nofollow">
  <title>CV link</title>
  <style>
    body { font-family: ui-monospace, monospace; max-width: 40rem; margin: 2rem auto; padding: 0 1rem; }
    label { display: block; margin: 1rem 0 0.25rem; }
    input[type=number] { width: 5rem; }
    button { margin-top: 1rem; }
    output { display: block; margin-top: 1.5rem; word-break: break-all; }
    .meta { color: #555; font-size: 0.9rem; margin-top: 0.5rem; }
  </style>
</head>
<body>
  <h1>CV access link</h1>
  <form method="post" action="/admin/">
    <label for="days">Valid for (days)</label>
    <input id="days" name="days" type="number" min="1" max="365" value="%d" required>
    <button type="submit">Generate</button>
  </form>
  %s
</body>
</html>
`
