package agents

// safePrefix returns at most n bytes of s. Use it instead of s[:n] on any
// string a client, agent or store can supply: slicing past the end panics,
// and a panic in a WebSocket goroutine takes the whole process down.
func safePrefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}
