package generator

import "testing"

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"VoiceChatService", "voice_chat_service"},
		{"UserService", "user_service"},
		{"API", "a_p_i"},
		{"getHTTPResponse", "get_h_t_t_p_response"},
		{"simple", "simple"},
	}
	for _, tt := range tests {
		got := toSnakeCase(tt.in)
		if got != tt.want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToScreamingSnake(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"VoiceChatService", "VOICE_CHAT_SERVICE"},
		{"UserService", "USER_SERVICE"},
	}
	for _, tt := range tests {
		got := toScreamingSnake(tt.in)
		if got != tt.want {
			t.Errorf("toScreamingSnake(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToLowerCamel(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"CreateVoiceChat", "createVoiceChat"},
		{"Get", "get"},
		{"", ""},
	}
	for _, tt := range tests {
		got := toLowerCamel(tt.in)
		if got != tt.want {
			t.Errorf("toLowerCamel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"user_id", "UserId"},
		{"chat_id", "ChatId"},
		{"simple", "Simple"},
	}
	for _, tt := range tests {
		got := toPascalCase(tt.in)
		if got != tt.want {
			t.Errorf("toPascalCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
