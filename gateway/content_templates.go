package main

import (
	"os"
	"path/filepath"
	"strings"
)

func registrationContentDir() string {
	return strings.TrimSpace(os.Getenv("REGISTRATION_CONTENT_DIR"))
}

func loadRegistrationTemplate(name string) (string, bool) {
	dir := registrationContentDir()
	if dir == "" {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func renderTemplate(tpl string, vars map[string]string) string {
	out := tpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}
