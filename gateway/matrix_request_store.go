package main

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

const pendingRequestMaxAge = 7 * 24 * time.Hour

type pendingRequest struct {
	Localpart string
	Email     string
	Display   string
	Source    string
	CreatedAt time.Time
	Ready     bool
	Token     string
}

var pendingRequests sync.Map

func newRequestID() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func waitSig(secret, rid string) string {
	return sign(secret, "wait:"+rid)
}

func validWaitSig(secret, rid, sig string) bool {
	return validPayloadSig(secret, "wait:"+rid, sig)
}

func putPendingRequest(rid string, pr *pendingRequest) {
	pendingRequests.Store(rid, pr)
}

func getPendingRequest(rid string) (*pendingRequest, bool) {
	v, ok := pendingRequests.Load(rid)
	if !ok {
		return nil, false
	}
	pr := v.(*pendingRequest)
	if time.Since(pr.CreatedAt) > pendingRequestMaxAge {
		pendingRequests.Delete(rid)
		return nil, false
	}
	return pr, true
}

func fulfillPendingRequest(rid, token, localpart string) {
	if rid == "" {
		return
	}
	v, ok := pendingRequests.Load(rid)
	if !ok {
		putPendingRequest(rid, &pendingRequest{
			Localpart: localpart,
			CreatedAt: time.Now(),
			Ready:     true,
			Token:     token,
		})
		return
	}
	pr := v.(*pendingRequest)
	pr.Ready = true
	pr.Token = token
	if localpart != "" {
		pr.Localpart = localpart
	}
	pendingRequests.Store(rid, pr)
}
