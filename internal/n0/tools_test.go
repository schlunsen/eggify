package n0

import "testing"

func TestIsProxiedTool(t *testing.T) {
	for name, want := range map[string]bool{
		"n0_app_status":                  true,
		"runpod__list_pods":              true,
		"ws__lunarrails__pascal__create": true,
		"ws--acme--crm__search":          true,
		"gpu_launch":                     false,
		"Bash":                           false,
		"__list_pods":                    false,
		"runpod__":                       false,
		"runpod__list pods":              false,
		"../x__y":                        false,
	} {
		if got := IsProxiedTool(name); got != want {
			t.Errorf("IsProxiedTool(%q) = %v, want %v", name, got, want)
		}
	}
}
