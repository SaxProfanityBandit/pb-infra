package main

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	joinGateCookieName = "matrix_join_gate"
	joinGateCookiePath = "/join"
)

func joinViaKuttTarget(slug string) string {
	return joinPublicBase() + "/via/kutt/" + slug
}

func setJoinGateCookie(w http.ResponseWriter, secret, expStr, sig string, exp int64) {
	maxAge := exp - time.Now().Unix()
	if maxAge < 1 {
		maxAge = 1
	}
	if !validSig(secret, expStr, sig) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     joinGateCookieName,
		Value:    expStr + "." + sig,
		Path:     joinGateCookiePath,
		MaxAge:   int(maxAge),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func validJoinGateCookie(r *http.Request, secret string) bool {
	c, err := r.Cookie(joinGateCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expStr, sig := parts[0], parts[1]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return false
	}
	return validSig(secret, expStr, sig)
}

// joinGateAllowedURI decides if /join (or POST /matrix/request) may proceed without Kutt entry.
func joinGateAllowedURI(originalURI, secret string) bool {
	u, err := url.Parse(originalURI)
	if err != nil {
		return false
	}
	q := u.Query()
	if strings.TrimSpace(q.Get("token")) != "" {
		return true
	}
	rid := strings.TrimSpace(q.Get("rid"))
	wsig := strings.TrimSpace(q.Get("wsig"))
	if rid != "" && wsig != "" && validWaitSig(secret, rid, wsig) {
		return true
	}
	return false
}

func joinGateAllowedRequest(r *http.Request, secret string) bool {
	orig := strings.TrimSpace(r.Header.Get("X-Original-URI"))
	if orig != "" && joinGateAllowedURI(orig, secret) {
		return true
	}
	if joinGateAllowedURI(r.URL.RequestURI(), secret) {
		return true
	}
	return validJoinGateCookie(r, secret)
}

func handleJoinKutt(w http.ResponseWriter, r *http.Request, slug, secret string, kutt *kuttClient) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	wantTarget := joinViaKuttTarget(slug)
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
	setJoinGateCookie(w, secret, expStr, sig, exp)
	http.Redirect(w, r, joinPublicBase(), http.StatusFound)
}

func handleJoinGate(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if joinGateAllowedRequest(r, secret) {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "Forbidden", http.StatusForbidden)
	}
}

func requireJoinGate(w http.ResponseWriter, r *http.Request, secret string) bool {
	if joinGateAllowedRequest(r, secret) {
		return true
	}
	http.Error(w, "Registration is only available through the invite link you received.", http.StatusForbidden)
	return false
}

func registerJoinKuttRoutes(mux *http.ServeMux, secret string, kutt *kuttClient) {
	mux.HandleFunc("/join/via/kutt/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/join/via/kutt/"), "/")
		if slug == "" || strings.Contains(slug, "/") {
			http.NotFound(w, r)
			return
		}
		handleJoinKutt(w, r, slug, secret, kutt)
	})
	mux.HandleFunc("/join/gate", handleJoinGate(secret))
	log.Print("Matrix join Kutt gate enabled (/join/via/kutt/{slug})")
}
