// Package handlers contains HTTP request handlers for the Eggify server.
// This file implements AI provider configuration endpoints.
package handlers

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/schlunsen/eggify/internal/connectors"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/llmclient"
	"github.com/schlunsen/eggify/internal/logging"
	"github.com/schlunsen/eggify/internal/providers"
)

// AgentDefaults holds the server-configured agent session defaults
// (from EGGIFY_AGENT_DEFAULT_PROVIDER, EGGIFY_AGENT_DEFAULT_MODEL, EGGIFY_AGENT_PERMISSION_MODE)
type AgentDefaults struct {
	DefaultProvider string `json:"default_provider,omitempty"`
	DefaultModel    string `json:"default_model,omitempty"`
	PermissionMode  string `json:"permission_mode,omitempty"`
}

// ProviderHandler handles AI provider configuration endpoints
type ProviderHandler struct {
	repo          *database.Repository
	registryCfg   providers.RegistryConfig
	agentDefaults AgentDefaults
}

// NewProviderHandler creates a new provider configuration handler
func NewProviderHandler(repo *database.Repository) *ProviderHandler {
	return &ProviderHandler{
		repo: repo,
	}
}

// SetAgentDefaults configures the server-side agent session defaults
// so the frontend can pre-populate the Create Session form.
func (h *ProviderHandler) SetAgentDefaults(defaults AgentDefaults) {
	h.agentDefaults = defaults
}

// SetRegistryConfig sets the registry configuration for manual sync operations
func (h *ProviderHandler) SetRegistryConfig(cfg providers.RegistryConfig) {
	h.registryCfg = cfg
}

// HandleGetProviders returns the list of available AI providers and current configuration
func (h *ProviderHandler) HandleGetProviders(c *fiber.Ctx) error {
	// Get all providers from database (includes both metadata and configuration)
	allProviders, err := h.repo.ListProviders()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to fetch providers from database",
		})
	}

	// Convert database providers to API response format
	var responseProviders []map[string]interface{}
	var currentProvider fiber.Map

	for _, dbProvider := range allProviders {
		// Parse models JSON
		var models []string
		if dbProvider.Models != nil && *dbProvider.Models != "" {
			if err := json.Unmarshal([]byte(*dbProvider.Models), &models); err != nil {
				// Log error but don't fail the whole request
				models = []string{}
			}
		}

		providerData := map[string]interface{}{
			"id":            dbProvider.ProviderID,
			"name":          dbProvider.Name,
			"icon":          dbProvider.Icon,
			"base_url":      dbProvider.BaseURL,
			"models":        models,
			"default_model": dbProvider.DefaultModel,
			"description":   dbProvider.Description,
			"enabled":       dbProvider.Enabled,
			"is_configured": dbProvider.IsConfigured,
			"is_current":    dbProvider.IsCurrent,
		}

		// If this provider is configured, include the configuration details
		if dbProvider.IsConfigured {
			if dbProvider.ModelName != nil {
				providerData["configured_model"] = *dbProvider.ModelName

				// For custom providers (or any provider with an empty models list),
				// add the configured model to the models array so it appears
				// in the session creation model dropdown
				if len(models) == 0 && *dbProvider.ModelName != "" {
					models = []string{*dbProvider.ModelName}
					providerData["models"] = models
				}
			}
			// SECURITY: never return the stored key; the UI only needs to
			// know one exists (an empty key on save keeps the stored one).
			if dbProvider.APIKey != nil && *dbProvider.APIKey != "" {
				providerData["has_api_key"] = true
				providerData["api_key_masked"] = connectors.MaskAPIKey(*dbProvider.APIKey)
			}
			if dbProvider.CustomURL != nil {
				providerData["custom_url"] = *dbProvider.CustomURL
			}
		}

		responseProviders = append(responseProviders, providerData)

		// Set current provider if this one is active
		if dbProvider.IsCurrent {
			currentProvider = redactedProviderConfig(dbProvider.ProviderID, dbProvider.APIKey, dbProvider.CustomURL, dbProvider.ModelName)
		}
	}

	response := fiber.Map{
		"providers": responseProviders,
		"count":     len(responseProviders),
		"current":   currentProvider,
		"timestamp": time.Now(),
	}

	// Include server-configured agent defaults so the frontend can
	// pre-populate the Create Session form with the right provider/model/mode
	if h.agentDefaults.DefaultProvider != "" || h.agentDefaults.DefaultModel != "" || h.agentDefaults.PermissionMode != "" {
		response["agent_defaults"] = h.agentDefaults
	}

	return c.JSON(response)
}

// redactedProviderConfig is the API view of a provider configuration: the
// same fields the old ProviderConfig JSON exposed, minus the key itself.
func redactedProviderConfig(providerID string, apiKey, customURL, modelName *string) fiber.Map {
	m := fiber.Map{"provider_id": providerID}
	if apiKey != nil && *apiKey != "" {
		m["has_api_key"] = true
		m["api_key_masked"] = connectors.MaskAPIKey(*apiKey)
	}
	if customURL != nil {
		m["custom_url"] = *customURL
	}
	if modelName != nil {
		m["model_name"] = *modelName
	}
	return m
}

// providerURLChanged reports whether a provider's custom URL moves to another
// server (scheme, host or port). Path-only edits on the same server keep the key.
func providerURLChanged(stored *string, next string) bool {
	prev := strings.TrimSpace(stringValue(stored))
	next = strings.TrimSpace(next)
	if prev == next {
		return false
	}
	if prev == "" || next == "" {
		return true
	}
	return llmclient.Origin(prev) != llmclient.Origin(next) || llmclient.Origin(next) == ""
}

// Helper to convert *string to string
func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SaveProviderRequest represents the request body for saving a provider configuration
type SaveProviderRequest struct {
	ProviderID string `json:"provider_id"`
	APIKey     string `json:"api_key"`
	CustomURL  string `json:"custom_url,omitempty"`
	ModelName  string `json:"model_name,omitempty"`
}

// HandleSaveProvider saves or updates a provider configuration
func (h *ProviderHandler) HandleSaveProvider(c *fiber.Ctx) error {
	var req SaveProviderRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Validate provider ID
	if req.ProviderID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Provider ID is required",
		})
	}

	req.ModelName = strings.TrimSpace(req.ModelName)
	if !providers.ValidModelName(req.ModelName) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid model name",
		})
	}

	// For custom provider, validate custom URL
	if req.ProviderID == "custom" && req.CustomURL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Custom URL is required for custom provider",
		})
	}

	// First fetch the provider to get its metadata (name, icon, base_url, models, etc.)
	dbProvider, err := h.repo.GetProvider(req.ProviderID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Provider not found: " + err.Error(),
		})
	}

	// SECURITY: custom URLs must be http(s), https unless loopback.
	if strings.TrimSpace(req.CustomURL) != "" {
		v, err := llmclient.ValidateBaseURL(req.CustomURL)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		req.CustomURL = v
	} else {
		req.CustomURL = ""
	}

	// The key is never sent to the browser, so an empty key on an
	// already-configured provider means "keep the stored key" -- but only
	// while the URL stays the same. SECURITY: otherwise a new custom_url
	// would make the provider proxy send the stored key to that host.
	if req.APIKey == "" && dbProvider.APIKey != nil && *dbProvider.APIKey != "" {
		if providerURLChanged(dbProvider.CustomURL, req.CustomURL) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Enter the API key again when changing the URL",
				"code":  "api_key_required_for_url_change",
			})
		}
		req.APIKey = *dbProvider.APIKey
	}

	// API key is required for all providers except Claude
	if req.APIKey == "" && req.ProviderID != "claude" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "API key is required",
		})
	}

	// Create configuration without automatically setting as default
	// Include the metadata from the database provider
	// Only set as current if it was already the default
	config := &database.ProviderConfig{
		ProviderID:   req.ProviderID,
		Name:         dbProvider.Name,
		Icon:         dbProvider.Icon,
		BaseURL:      dbProvider.BaseURL,
		Models:       dbProvider.Models,
		DefaultModel: dbProvider.DefaultModel,
		Description:  dbProvider.Description,
		APIKey:       &req.APIKey,
		CustomURL:    &req.CustomURL,
		ModelName:    &req.ModelName,
		IsCurrent:    dbProvider.IsCurrent, // Keep existing default status
	}

	// Save to database (sets it as current provider)
	if err := providers.SaveProviderConfig(h.repo, config); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save provider configuration: " + err.Error(),
		})
	}

	// Generate environment script
	if err := providers.GenerateEnvScript(config); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate environment script: " + err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Provider configuration saved successfully",
		"config":  redactedProviderConfig(config.ProviderID, config.APIKey, config.CustomURL, config.ModelName),
	})
}

// HandleSetProviderAsDefault sets a provider as the default
func (h *ProviderHandler) HandleSetProviderAsDefault(c *fiber.Ctx) error {
	providerID := c.Params("id")
	if providerID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Provider ID is required",
		})
	}

	// Fetch the provider to verify it exists and is configured
	dbProvider, err := h.repo.GetProvider(providerID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Provider not found",
		})
	}

	if !dbProvider.IsConfigured {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Provider must be configured before setting as default",
		})
	}

	// Create a config to set as current (default)
	config := &database.ProviderConfig{
		ProviderID: providerID,
		APIKey:     dbProvider.APIKey,
		CustomURL:  dbProvider.CustomURL,
		ModelName:  dbProvider.ModelName,
		IsCurrent:  true, // Set as default
	}

	// Save to database (this will unset other providers as current)
	if err := h.repo.SaveProvider(config); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to set provider as default: " + err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Provider set as default successfully",
	})
}

// HandleDeleteProvider deletes the current provider configuration
func (h *ProviderHandler) HandleDeleteProvider(c *fiber.Ctx) error {
	// Delete current provider configuration
	if err := providers.DeleteProviderConfig(h.repo); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to delete provider configuration",
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Provider configuration deleted successfully",
	})
}

// HandleDeleteProviderByID clears one provider's key, URL and model. Other
// providers are left alone.
func (h *ProviderHandler) HandleDeleteProviderByID(c *fiber.Ctx) error {
	providerID := c.Params("id")
	if providerID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Provider id is required",
		})
	}

	// The env script holds the current provider's key, so it goes with it.
	current, _ := h.repo.GetCurrentProvider()
	wasCurrent := current != nil && current.ProviderID == providerID

	if err := h.repo.DeleteProvider(providerID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to delete provider configuration",
		})
	}
	if wasCurrent {
		if err := os.Remove(providers.GetEnvScriptPath()); err != nil && !os.IsNotExist(err) {
			logging.Warning("Failed to remove provider env script: %v", err)
		}
	}

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Provider configuration deleted successfully",
	})
}

// HandleSyncModels triggers a manual sync of the model registry from the remote URL.
// This allows users to refresh the available models without restarting the server.
func (h *ProviderHandler) HandleSyncModels(c *fiber.Ctx) error {
	if !h.registryCfg.Enabled {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":   "Model registry sync is disabled",
			"message": "Remote model sync is not enabled in server configuration",
		})
	}

	// Force a fresh fetch by using a config with no cache TTL consideration
	// (SyncModelsFromRegistry will fetch from remote since we're explicitly requesting it)
	result := providers.SyncModelsFromRegistry(h.repo, h.registryCfg)

	response := fiber.Map{
		"success":         result.Error == nil,
		"source":          result.Source,
		"providers_count": result.ProvidersCount,
		"models_updated":  result.ModelsUpdated,
		"timestamp":       result.LastFetched,
	}

	if result.Error != nil {
		response["warning"] = result.Error.Error()
	}

	return c.JSON(response)
}
