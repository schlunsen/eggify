package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/schlunsen/eggify/internal/capability"
)

// appsEnabled: the app subdomain feature needs EGGIFY_SUBDOMAIN and
// EGGIFY_APPS_BASE_DOMAIN (no default), like handlers.AppsEnabled.
func appsEnabled() bool {
	return strings.TrimSpace(os.Getenv("EGGIFY_SUBDOMAIN")) != "" && strings.Trim(os.Getenv("EGGIFY_APPS_BASE_DOMAIN"), ". ") != ""
}

// appsToolDefinitions returns MCP tool definitions for app management.
// Listed only when appsEnabled(); the handlers check it again.
func appsToolDefinitions() []Tool {
	subdomain := os.Getenv("EGGIFY_SUBDOMAIN")
	if subdomain == "" {
		subdomain = "sandbox" // placeholder for tool descriptions when not in sandbox
	}
	base := os.Getenv("EGGIFY_APPS_BASE_DOMAIN")
	if base == "" {
		base = "example.com" // placeholder, as above
	}

	return []Tool{
		{
			Name:        "deploy_app",
			Description: fmt.Sprintf("Deploy a web app to a subdomain of %s.%s. Registers an internal port so that {name}.%s.%s routes to it. The app must already be running (e.g. via 'npx serve dist -l 4321 &') on the specified port before calling this tool.", subdomain, base, subdomain, base),
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"name": {
						Type:        "string",
						Description: fmt.Sprintf("Subdomain name for the app (e.g. 'myapp' becomes myapp.%s.%s). Must be lowercase alphanumeric with optional hyphens.", subdomain, base),
					},
					"port": {
						Type:        "number",
						Description: "Internal port the app is listening on (1024-65535)",
					},
				},
				Required: []string{"name", "port"},
			},
		},
		{
			Name:        "list_deployed_apps",
			Description: fmt.Sprintf("List all deployed apps and their URLs on %s.%s", subdomain, base),
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
				Required:   []string{},
			},
		},
		{
			Name:        "undeploy_app",
			Description: "Remove a deployed app subdomain registration",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"name": {
						Type:        "string",
						Description: "Name of the app to remove",
					},
				},
				Required: []string{"name"},
			},
		},
	}
}

// eggifyAPIBase returns the base URL for the local eggify server API
func eggifyAPIBase() string {
	port := os.Getenv("EGGIFY_PORT")
	if port == "" {
		port = "3333"
	}
	return fmt.Sprintf("http://127.0.0.1:%s", port)
}

// appsRequest calls the Eggify server's loopback /internal/apps routes with this
// session's apps capability (set by the server in the eggify-tools environment).
// Without it the server refuses: those routes are pre-auth, so the capability
// is what stops any other local process from publishing ports to the internet.
func appsRequest(method, path string, body io.Reader) (*http.Response, error) {
	sid := os.Getenv(capability.EnvSessionID)
	token := os.Getenv(capability.EnvAppsToken)
	if sid == "" || token == "" {
		return nil, fmt.Errorf("app deployment is only available from an Eggify agent session")
	}
	req, err := http.NewRequest(method, eggifyAPIBase()+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(capability.HeaderSessionID, sid)
	return http.DefaultClient.Do(req)
}

func toolDeployApp(args map[string]interface{}) CallToolResult {
	subdomain, base := os.Getenv("EGGIFY_SUBDOMAIN"), strings.Trim(os.Getenv("EGGIFY_APPS_BASE_DOMAIN"), ". ")
	if !appsEnabled() {
		return mcpErrorResult("App deployment not available (needs EGGIFY_SUBDOMAIN and EGGIFY_APPS_BASE_DOMAIN, set by a hosted sandbox)")
	}

	name, _ := args["name"].(string)
	if name == "" {
		return mcpErrorResult("name is required")
	}

	portFloat, _ := args["port"].(float64)
	port := int(portFloat)
	if port < 1024 || port > 65535 {
		return mcpErrorResult("port must be between 1024 and 65535")
	}

	// Call the local eggify server API
	body, _ := json.Marshal(map[string]interface{}{"name": name, "port": port})
	resp, err := appsRequest("POST", "/internal/apps", bytes.NewReader(body))
	if err != nil {
		return mcpErrorResult(fmt.Sprintf("Failed to register app: %v", err))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return mcpErrorResult(fmt.Sprintf("Failed to register app: %s", string(respBody)))
	}

	url := fmt.Sprintf("https://%s.%s.%s", name, subdomain, base)
	result := map[string]interface{}{
		"success": true,
		"name":    name,
		"port":    port,
		"url":     url,
		"message": fmt.Sprintf("App deployed! Live at %s", url),
	}

	data, _ := json.Marshal(result)
	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: string(data)}},
	}
}

func toolListDeployedApps(args map[string]interface{}) CallToolResult {
	if !appsEnabled() {
		return mcpErrorResult("App deployment not available (needs EGGIFY_SUBDOMAIN and EGGIFY_APPS_BASE_DOMAIN, set by a hosted sandbox)")
	}

	resp, err := appsRequest("GET", "/internal/apps", nil)
	if err != nil {
		return mcpErrorResult(fmt.Sprintf("Failed to list apps: %v", err))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: string(respBody)}},
	}
}

func toolUndeployApp(args map[string]interface{}) CallToolResult {
	if !appsEnabled() {
		return mcpErrorResult("App deployment not available (needs EGGIFY_SUBDOMAIN and EGGIFY_APPS_BASE_DOMAIN, set by a hosted sandbox)")
	}

	name, _ := args["name"].(string)
	if name == "" {
		return mcpErrorResult("name is required")
	}

	resp, err := appsRequest("DELETE", "/internal/apps/"+url.PathEscape(name), nil)
	if err != nil {
		return mcpErrorResult(fmt.Sprintf("Failed to remove app: %v", err))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return mcpErrorResult(fmt.Sprintf("Failed to remove app: %s", string(respBody)))
	}

	result := map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("App '%s' removed", name),
	}

	data, _ := json.Marshal(result)
	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: string(data)}},
	}
}
