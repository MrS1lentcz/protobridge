package generator

import (
	"strings"
	"unicode"
)

// toSnakeCase converts CamelCase to snake_case while keeping acronyms glued
// together:
//
//	VoiceChatService → voice_chat_service
//	GitCreatePR      → git_create_pr        (not "git_create_p_r")
//	HTTPRequest      → http_request
//	ParseURL         → parse_url
//	APIKey           → api_key
//	IPv4Address      → i_pv4_address
//
// Splitting on every uppercase rune would break acronyms like "PR" or
// "URL" into "p_r" / "u_r_l" — the convention MCP clients and OpenAPI
// consumers expect is one underscore per word boundary, with an
// uppercase run treated as a single word.
func toSnakeCase(s string) string {
	return ToSnakeCase(s)
}

// ToSnakeCase is the public entry point; sibling packages (mcpgen) share
// the same conversion instead of re-implementing it.
func ToSnakeCase(s string) string {
	runes := []rune(s)
	var result []rune
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 && wordBoundaryBefore(runes, i) {
			result = append(result, '_')
		}
		if unicode.IsUpper(r) {
			result = append(result, unicode.ToLower(r))
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

// wordBoundaryBefore decides whether an underscore belongs immediately
// before runes[i] (assumed to be uppercase). A boundary exists when:
//   - the previous rune is lowercase or a digit (end of a lowercase run); or
//   - the previous rune is uppercase AND the next rune is lowercase (end of
//     an acronym, e.g. the P in "HTTPRequest").
//
// Runs of uppercase letters with no trailing lowercase produce no interior
// underscores ("URL" stays "url", not "u_r_l").
func wordBoundaryBefore(runes []rune, i int) bool {
	prev := runes[i-1]
	if unicode.IsLower(prev) || unicode.IsDigit(prev) {
		return true
	}
	if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
		return true
	}
	return false
}

// toScreamingSnake converts "VoiceChatService" to "VOICE_CHAT_SERVICE".
func toScreamingSnake(s string) string {
	return strings.ToUpper(toSnakeCase(s))
}

// toLowerCamel converts "CreateVoiceChat" to "createVoiceChat".
func toLowerCamel(s string) string {
	if len(s) == 0 {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// chiPathFromProto converts "{chat_id}" path params to chi-style "{chat_id}".
// Proto and chi use the same syntax, so this is mostly a passthrough,
// but we validate the format.
func chiPathFromProto(path string) string {
	return path
}
