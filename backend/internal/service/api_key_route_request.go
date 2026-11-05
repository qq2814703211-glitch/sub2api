package service

import "strings"

// APIKeyRouteSelectionRequest identifies the endpoints that perform final-group
// admission. Other endpoints keep their existing authentication/billing checks.
func APIKeyRouteSelectionRequest(method, path string) bool {
	if method == "GET" {
		return strings.HasSuffix(path, "/responses")
	}
	if method != "POST" {
		return false
	}
	for _, suffix := range []string{"/messages", "/responses", "/chat/completions", "/embeddings", "/images/generations", "/images/edits", "/alpha/search", "/cachedContents", "/web_search", "/x_search", "/tts", "/stt", "/custom-voices", "/systemone"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return strings.Contains(path, "/models/") && strings.Contains(path, ":") || strings.Contains(path, "/responses/") || strings.HasSuffix(path, "/videos") || strings.HasSuffix(path, "/videos/generations") || strings.HasSuffix(path, "/videos/edits") || strings.HasSuffix(path, "/videos/extensions")
}
