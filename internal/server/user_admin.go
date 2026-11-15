package server

// MayAdminister reports whether username may perform privileged actions
// (principal.IsAdmin): the user is an admin, or the only account. With
// several accounts a non-admin user is not the owner of the machine, while
// privileged actions (the terminal, YOLO, unsandboxed sessions, hooks, MCP
// servers, providers, remote access, ...) act as the server's OS user.
func (us *UserStore) MayAdminister(username string) bool {
	if us == nil || username == "" {
		return false
	}
	us.mu.RLock()
	defer us.mu.RUnlock()
	user, ok := us.users[username]
	if !ok || user == nil {
		return false
	}
	return user.IsAdmin || len(us.users) == 1
}
