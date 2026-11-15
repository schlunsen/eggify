package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func listedTools(t *testing.T) string {
	t.Helper()
	r := NewMCPServer(nil).HandleRequest(MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	b, _ := json.Marshal(r)
	return string(b)
}

// The app tools are only listed when the hosted sandbox configured apps.
func TestAppToolsListedOnlyWhenAppsEnabled(t *testing.T) {
	t.Setenv("EGGIFY_SUBDOMAIN", "xyz2")
	t.Setenv("EGGIFY_APPS_BASE_DOMAIN", ".")
	if strings.Contains(listedTools(t), "deploy_app") {
		t.Error("deploy_app listed without a usable base domain")
	}
	if out := toolUndeployApp(map[string]interface{}{"name": "a"}); out.IsError == nil || !*out.IsError {
		t.Error("undeploy_app ran without a base domain")
	}
	t.Setenv("EGGIFY_APPS_BASE_DOMAIN", "example.com")
	if !strings.Contains(listedTools(t), "deploy_app") {
		t.Error("deploy_app missing with both variables set")
	}
}
