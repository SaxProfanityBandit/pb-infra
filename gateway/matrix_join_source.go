package main

import (
	"net/url"
	"regexp"
	"strings"
)

var matrixJoinSourceRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

var kuttSlugToJoinSource map[string]string

func initJoinSourcesFromEnv() {
	raw := strings.TrimSpace(envOr("MATRIX_KUTT_SLUG_SOURCES", ""))
	kuttSlugToJoinSource = map[string]string{}
	if raw == "" {
		return
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || k == "" || v == "" || !matrixJoinSourceRE.MatchString(v) {
			continue
		}
		kuttSlugToJoinSource[k] = v
	}
}

func joinSourceForKuttSlug(slug string) string {
	if kuttSlugToJoinSource == nil {
		initJoinSourcesFromEnv()
	}
	return kuttSlugToJoinSource[strings.TrimSpace(slug)]
}

func joinRedirectAfterKutt(slug string) string {
	base := joinPublicBase()
	if src := joinSourceForKuttSlug(slug); src != "" {
		return base + "?source=" + url.QueryEscape(src)
	}
	return base
}

func normalizeJoinSource(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || !matrixJoinSourceRE.MatchString(s) {
		return ""
	}
	return s
}

func joinSourceDisplayLabel(source string) string {
	switch source {
	case "discord-migration":
		return "Discord migration"
	default:
		return source
	}
}
