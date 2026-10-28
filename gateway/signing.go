package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

func validSig(secret, expStr, sig string) bool {
	expected := sign(secret, expStr)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) == 1
}

func sign(secret, expStr string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(expStr))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
