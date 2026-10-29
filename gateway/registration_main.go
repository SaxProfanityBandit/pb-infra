package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	secret := os.Getenv("LINK_SIGNING_KEY")
	if secret == "" {
		log.Fatal("LINK_SIGNING_KEY is not set")
	}
	publicRoot := envOr("PUBLIC_ROOT", "/srv/public")
	addr := envOr("LISTEN", ":8080")

	mux := http.NewServeMux()
	initJoinSourcesFromEnv()
	ma := loadMatrixApproveConfig("")
	if ma == nil {
		log.Fatal("registration gateway needs SYNAPSE_ADMIN_ACCESS_TOKEN")
	}
	mux.HandleFunc("/matrix/approve", handleMatrixApprove(secret, ma))
	mrc := loadMatrixRequestConfig(secret, ma)
	if mrc == nil {
		log.Fatal("registration gateway needs CONTACT_SMTP_USER for access requests")
	}
	mux.HandleFunc("/matrix/request", handleMatrixRequest(mrc))
	mux.HandleFunc("/matrix/request/status", handleMatrixRequestStatus(mrc))
	kuttURL := strings.TrimRight(os.Getenv("KUTT_API_URL"), "/")
	kuttKey := os.Getenv("KUTT_API_KEY")
	if kuttURL != "" && kuttKey != "" {
		kutt := &kuttClient{base: kuttURL, apiKey: kuttKey, http: http.DefaultClient}
		registerJoinKuttRoutes(mux, secret, kutt)
	} else {
		log.Print("Matrix join Kutt gate disabled (set KUTT_API_URL and KUTT_API_KEY)")
	}
	mux.Handle("/", http.FileServer(http.Dir(publicRoot)))

	s := &http.Server{
		Addr:              addr,
		Handler:           withRobots(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("registration gateway listening on %s", addr)
	log.Fatal(s.ListenAndServe())
}
