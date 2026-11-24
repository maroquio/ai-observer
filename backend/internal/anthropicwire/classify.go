package anthropicwire

import "strings"

// Paths of the Messages API endpoints ClassifyEndpoint recognizes
const (
	PathMessages    = "/v1/messages"
	PathCountTokens = "/v1/messages/count_tokens"
)

// ClassifyEndpoint tells what an exchange is from its method and URL path
// (without query: Claude Code sends "/v1/messages?beta=true", the caller
// strips "?beta=true"). One trailing slash is tolerated; the method is
// compared case-insensitively.
func ClassifyEndpoint(method, path string) ExchangeKind {
	if !strings.EqualFold(method, "POST") {
		return KindOtherAPI
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	switch path {
	case PathMessages:
		return KindModelInference
	case PathCountTokens:
		return KindTokenCount
	}
	return KindOtherAPI
}
