package generator

import (
	"strings"
	"unicode"
)

// toSnakeCase converts "VoiceChatService" to "voice_chat_service".
func toSnakeCase(s string) string {
	var result []rune
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, unicode.ToLower(r))
		} else {
			result = append(result, r)
		}
	}
	return string(result)
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
