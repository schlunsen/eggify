package llmclient

import (
	"encoding/json"
	"strings"
	"unicode"
)

// maxAPIErrorLen caps the upstream error text shown in chat errors.
const maxAPIErrorLen = 200

// apiErrorSummary turns an upstream error body into a short, single-line
// message for chat errors. The body comes from whatever server the base URL
// points at, so it is not echoed wholesale: the "message" of an
// OpenAI/Anthropic-style error is used when present, control characters are
// removed, the client's API key is redacted and the result is capped.
func (c *Client) apiErrorSummary(body []byte) string {
	msg := extractAPIErrorMessage(body)
	if c != nil && len(c.apiKey) >= 8 {
		msg = strings.ReplaceAll(msg, c.apiKey, "[redacted]")
	}
	msg = sanitizeErrorText(msg)
	if msg == "" {
		return "(no error message)"
	}
	return msg
}

func extractAPIErrorMessage(body []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
	}
	if json.Unmarshal(body, &v) == nil {
		if len(v.Error) > 0 {
			var inner struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			}
			if json.Unmarshal(v.Error, &inner) == nil && inner.Message != "" {
				return inner.Message
			}
			var s string
			if json.Unmarshal(v.Error, &s) == nil && s != "" {
				return s
			}
		}
		if v.Message != "" {
			return v.Message
		}
		if v.Detail != "" {
			return v.Detail
		}
	}
	return string(body)
}

// sanitizeErrorText collapses whitespace, drops control and format
// characters and caps the length.
func sanitizeErrorText(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = true
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == unicode.ReplacementChar:
			continue
		}
		space = false
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if r := []rune(out); len(r) > maxAPIErrorLen {
		out = string(r[:maxAPIErrorLen]) + "…"
	}
	return out
}
