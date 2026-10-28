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
	"os/exec"
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
	mux.HandleFunc("/cv/resume.pdf", func(w http.ResponseWriter, r *http.Request) {
		handleCVPDF(w, r, secret, addr)
	})
	mux.Handle("/cv/", gateCV(secret, privateCV))
	if contact := loadContactConfig(); contact != nil {
		mux.HandleFunc("/contact", handleContact(contact))
		log.Print("Contact form enabled (POST /contact)")
	} else {
		log.Print("Contact form disabled (set CONTACT_SMTP_USER and secret)")
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

func gateCV(secret, privateCV string) http.Handler {
	inner := http.StripPrefix("/cv/", http.FileServer(http.Dir(privateCV)))
	indexPath := filepath.Join(privateCV, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/cv/") && r.URL.Path != "/cv" {
			http.NotFound(w, r)
			return
		}
		if err := authorizeCV(r, secret); err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		clean := filepath.Clean(r.URL.Path)
		if clean != "/cv" && clean != "/cv/" && !strings.HasPrefix(clean, "/cv/") {
			http.NotFound(w, r)
			return
		}

		rel := strings.TrimPrefix(r.URL.Path, "/cv/")
		rel = strings.Trim(rel, "/")
		if rel == "" {
			rel = "index.html"
		}
		if rel == "index.html" && r.URL.Query().Get("pdf_access") != "" {
			serveCVPDFHTML(w, r, indexPath, privateCV)
			return
		}

		inner.ServeHTTP(w, r)
	})
}

func pdfLang(r *http.Request) string {
	if r.URL.Query().Get("lang") == "en" {
		return "en"
	}
	return "sv"
}

func serveCVPDFHTML(w http.ResponseWriter, r *http.Request, indexPath, privateCV string) {
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := pdfLang(r)
	html := string(raw)

	printCSSPath := filepath.Join(privateCV, "css", "print.css")
	if css, err := os.ReadFile(printCSSPath); err == nil {
		inline := "<style id=\"pdf-inline-print\">\n" + string(css) + "\n</style>"
		if i := strings.Index(html, "</head>"); i >= 0 {
			html = html[:i] + inline + html[i:]
		}
	}

	langScript := fmt.Sprintf(
		`<script>(function(){var lang=%q;document.documentElement.lang=lang;document.querySelectorAll(".lang-block").forEach(function(el){el.hidden=el.getAttribute("data-lang")!==lang;});})();</script>`,
		lang,
	)
	if i := strings.LastIndex(html, "</body>"); i >= 0 {
		html = html[:i] + langScript + html[i:]
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte(html))
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

func authorizeCV(r *http.Request, secret string) error {
	if tok := r.URL.Query().Get("pdf_access"); tok != "" {
		if validPDFAccess(secret, tok) {
			return nil
		}
	}
	return authorizeRequest(r, secret)
}

func validPDFAccess(secret, tok string) bool {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expStr, sig := parts[0], parts[1]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > exp {
		return false
	}
	return validSig(secret, "pdf:"+expStr, sig)
}

func makePDFAccessToken(secret string, exp int64) string {
	expStr := strconv.FormatInt(exp, 10)
	return expStr + "." + sign(secret, "pdf:"+expStr)
}

func listenPort(addr string) string {
	port := strings.TrimPrefix(addr, ":")
	if port == "" {
		return "8080"
	}
	return port
}

func chromiumBinary() string {
	if p := os.Getenv("CHROMIUM_PATH"); p != "" {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func handleCVPDF(w http.ResponseWriter, r *http.Request, secret, listenAddr string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := authorizeRequest(r, secret); err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	chrome := chromiumBinary()
	if chrome == "" {
		http.Error(w, "PDF export unavailable", http.StatusServiceUnavailable)
		return
	}

	exp := time.Now().Add(2 * time.Minute).Unix()
	tok := makePDFAccessToken(secret, exp)
	port := listenPort(listenAddr)
	lang := pdfLang(r)
	pageURL := fmt.Sprintf(
		"http://127.0.0.1:%s/cv/?pdf_access=%s&lang=%s",
		port,
		url.QueryEscape(tok),
		url.QueryEscape(lang),
	)

	tmp, err := os.CreateTemp("", "cv-resume-*.pdf")
	if err != nil {
		http.Error(w, "PDF failed", http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--run-all-compositor-stages-before-draw",
		"--virtual-time-budget=30000",
		"--prefer-css-page-size",
		"--no-pdf-header-footer",
		"--print-to-pdf=" + tmpPath,
		pageURL,
	}
	cmd := exec.CommandContext(ctx, chrome, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("chromium pdf: %v: %s", err, strings.TrimSpace(string(out)))
		http.Error(w, "PDF failed", http.StatusInternalServerError)
		return
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil || len(data) == 0 {
		http.Error(w, "PDF failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="Rickard_Sahlgren_CV.pdf"`)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(data)
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
