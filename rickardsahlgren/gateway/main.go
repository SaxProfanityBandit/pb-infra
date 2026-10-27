package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
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
	addr := envOr("LISTEN", ":8080")

	mux := http.NewServeMux()
	mux.HandleFunc("/access", func(w http.ResponseWriter, r *http.Request) {
		handleAccess(w, r, secret)
	})
	cvFS := http.FileServer(http.Dir(privateCV))
	mux.Handle("/cv/", gateStatic(secret, http.StripPrefix("/cv/", cvFS), "/cv/"))
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
