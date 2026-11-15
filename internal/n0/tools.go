package n0

import (
	"regexp"
	"strings"
)

// connectorToolName matches n0's workspace connector tools, which it names
// <slug>__<tool> (runpod__list_pods, ws__acme__crm__search). n0 lists them
// only for connections the user granted workspace:connectors on the consent
// screen, and re-checks the user's access on every call.
var connectorToolName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*__[A-Za-z0-9][A-Za-z0-9_-]*$`)

// IsProxiedTool reports whether a tool name belongs to n0 and is forwarded
// to it: n0's own n0_* tools, or a workspace connector tool (<slug>__<tool>).
// Eggify's own tools never contain "__", so the two sets can't collide.
func IsProxiedTool(name string) bool {
	return strings.HasPrefix(name, "n0_") || connectorToolName.MatchString(name)
}
