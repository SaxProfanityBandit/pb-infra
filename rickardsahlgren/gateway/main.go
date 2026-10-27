package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
	kuttURL := strings.TrimRight(os.Getenv("KUTT_API_URL"), "/")
	kuttKey := os.Getenv("KUTT_API_KEY")

	mux := http.NewServeMux()
	mux.HandleFunc("/access", func(w http.ResponseWriter, r *http.Request) {
		handleAccess(w, r, secret)
	})
	if kuttURL != "" && kuttKey != "" {
		kutt := &kuttClient{base: kuttURL, apiKey: kuttKey, http: http.DefaultClient}
		mux.HandleFunc("/access/kutt/", func(w http.ResponseWriter, r *http.Request) {
			slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/access/kutt/"), "/")
			if slug == "" || strings.Contains(slug, "/") {
				http.NotFound(w, r)
				return
			}
			handleAccessKutt(w, r, slug, secret, baseURL, kutt)
		})
		log.Printf("Kutt CV access enabled (API %s)", kuttURL)
	} else {
		log.Print("Kutt CV access disabled (set KUTT_API_URL and KUTT_API_KEY)")
	}
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

type kuttClient struct {
	base   string
	apiKey string
	http   *http.Client
}

type kuttLink struct {
	Address  string `json:"address"`
	Target   string `json:"target"`
	Banned   bool   `json:"banned"`
	ExpireIn string `json:"expire_in"`
}

type kuttLinksResponse struct {
	Data []kuttLink `json:"data"`
}

func (c *kuttClient) linkByAddress(ctx context.Context, address string) (*kuttLink, error) {
	u := c.base + "/api/v2/links?limit=50&search=" + url.QueryEscape(address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kutt api: %s", resp.Status)
	}
	var body kuttLinksResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	for i := range body.Data {
		if body.Data[i].Address == address {
			return &body.Data[i], nil
		}
	}
	return nil, errors.New("link not found")
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
	if !validSig(secret, expStr, sig) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	setGateCookie(w, secret, expStr, sig, exp)
	http.Redirect(w, r, "/cv/", http.StatusFound)
}

func handleAccessKutt(w http.ResponseWriter, r *http.Request, slug, secret, baseURL string, kutt *kuttClient) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	wantTarget := baseURL + "/access/kutt/" + slug
	link, err := kutt.linkByAddress(r.Context(), slug)
	if err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if link.Banned || link.Target != wantTarget {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	exp, err := linkExpiryUnix(link.ExpireIn)
	if err != nil || time.Now().Unix() >= exp {
		http.Error(w, "Link expired", http.StatusForbidden)
		return
	}
	expStr := strconv.FormatInt(exp, 10)
	sig := sign(secret, expStr)
	setGateCookie(w, secret, expStr, sig, exp)
	http.Redirect(w, r, "/cv/", http.StatusFound)
}

func linkExpiryUnix(expireIn string) (int64, error) {
	if expireIn == "" {
		return time.Now().Unix() + 14*86400, nil
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, expireIn); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, errors.New("bad expire_in")
}

func setGateCookie(w http.ResponseWriter, secret, expStr, sig string, exp int64) {
	maxAge := exp - time.Now().Unix()
	if maxAge < 1 {
		maxAge = 1
	}
	if !validSig(secret, expStr, sig) {
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
