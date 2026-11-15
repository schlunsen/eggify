// Package providers - registry.go implements a remote model registry
// that fetches up-to-date provider/model lists from a remote URL on startup.
//
// Fallback chain: Remote URL → Local cache → Embedded JSON (compile-time)
package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/schlunsen/eggify/internal/database"
)

const (
	// DefaultRegistryURL is the default URL for the remote model registry.
	// This can be hosted on GitHub Pages, a CDN, or any static file host.
	DefaultRegistryURL = "https://schlunsen.github.io/eggify/models-registry.json"

	// registryCacheFile is the local cache filename stored in the claude config dir
	registryCacheFile = "models-registry.json"

	// registryFetchTimeout is the maximum time to wait for the remote registry
	registryFetchTimeout = 5 * time.Second

	// registryCacheTTL is how long the local cache is considered fresh (skip remote fetch)
	registryCacheTTL = 1 * time.Hour
)

// RegistryConfig holds configuration for the model registry
type RegistryConfig struct {
	// URL is the remote registry URL to fetch from
	URL string

	// CacheDir is the directory to store the local cache file
	CacheDir string

	// Enabled controls whether remote sync is active (can be disabled for air-gapped environments)
	Enabled bool
}

// RegistrySyncResult contains the outcome of a sync operation
type RegistrySyncResult struct {
	Source         string    // "remote", "cache", "embedded"
	ProvidersCount int       // Number of providers synced
	ModelsUpdated  int       // Number of provider model lists updated
	LastFetched    time.Time // When the data was fetched
	Error          error     // Non-fatal error (sync still succeeded via fallback)
}

// SyncModelsFromRegistry fetches the latest provider/model definitions and merges them into the database.
// It uses a 3-tier fallback: remote URL → local cache → embedded JSON.
// Only model metadata is updated; user configuration (api_key, is_current, etc.) is preserved.
func SyncModelsFromRegistry(repo *database.Repository, config RegistryConfig) *RegistrySyncResult {
	result := &RegistrySyncResult{
		LastFetched: time.Now(),
	}

	if !config.Enabled {
		result.Source = "disabled"
		return result
	}

	// Ensure cache directory exists
	if config.CacheDir != "" {
		os.MkdirAll(config.CacheDir, 0755)
	}

	cachePath := filepath.Join(config.CacheDir, registryCacheFile)

	// Check if cache is fresh enough to skip remote fetch
	if isCacheFresh(cachePath) {
		providers, err := loadFromFile(cachePath)
		if err == nil {
			updated := mergeProvidersIntoDB(repo, providers)
			result.Source = "cache"
			result.ProvidersCount = len(providers)
			result.ModelsUpdated = updated
			return result
		}
		// Cache unreadable, fall through to remote
	}

	// Tier 1: Try remote URL
	registryURL := config.URL
	if registryURL == "" {
		registryURL = DefaultRegistryURL
	}

	providers, err := fetchRemoteRegistry(registryURL)
	if err == nil {
		// Save to local cache for offline use
		saveToCache(cachePath, providers)

		updated := mergeProvidersIntoDB(repo, providers)
		result.Source = "remote"
		result.ProvidersCount = len(providers)
		result.ModelsUpdated = updated
		return result
	}

	result.Error = fmt.Errorf("remote fetch failed: %w", err)

	// Tier 2: Try local cache (even if stale)
	providers, err = loadFromFile(cachePath)
	if err == nil {
		updated := mergeProvidersIntoDB(repo, providers)
		result.Source = "cache"
		result.ProvidersCount = len(providers)
		result.ModelsUpdated = updated
		return result
	}

	// Tier 3: Use embedded JSON (compile-time fallback, already in DB from migrations)
	result.Source = "embedded"
	result.ProvidersCount = len(GetAvailableProviders())
	return result
}

// SyncModelsAsync runs the registry sync in a background goroutine.
// Returns immediately. The result can be retrieved via the returned channel.
func SyncModelsAsync(repo *database.Repository, config RegistryConfig) <-chan *RegistrySyncResult {
	ch := make(chan *RegistrySyncResult, 1)
	go func() {
		result := SyncModelsFromRegistry(repo, config)
		ch <- result
		close(ch)
	}()
	return ch
}

// fetchRemoteRegistry fetches the provider registry from a remote URL
func fetchRemoteRegistry(url string) ([]Provider, error) {
	client := &http.Client{
		Timeout: registryFetchTimeout,
	}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch registry from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned status %d from %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("failed to read registry response: %w", err)
	}

	return parseProvidersJSON(body)
}

// loadFromFile reads a providers JSON file from disk
func loadFromFile(path string) ([]Provider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseProvidersJSON(data)
}

// parseProvidersJSON parses the providers JSON format
func parseProvidersJSON(data []byte) ([]Provider, error) {
	var config ProvidersConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse providers JSON: %w", err)
	}
	if len(config.Providers) == 0 {
		return nil, fmt.Errorf("providers JSON contains no providers")
	}
	return config.Providers, nil
}

// saveToCache writes the providers data to the local cache file
func saveToCache(path string, providers []Provider) {
	config := ProvidersConfig{Providers: providers}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return // Best effort, don't fail
	}
	os.WriteFile(path, data, 0644)
}

// isCacheFresh checks if the local cache file exists and is within the TTL
func isCacheFresh(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < registryCacheTTL
}

// mergeProvidersIntoDB updates provider model metadata in the database
// without touching user configuration (api_key, is_current, custom_url, etc.)
func mergeProvidersIntoDB(repo *database.Repository, providers []Provider) int {
	updated := 0

	providers = withEmbeddedModels(providers, GetAvailableProviders())
	for _, provider := range providers {
		// Skip custom provider - user manages this entirely
		if provider.ID == "custom" {
			continue
		}

		modelsJSON, err := json.Marshal(provider.Models)
		if err != nil {
			continue
		}

		err = repo.UpdateProviderModels(
			provider.ID,
			string(modelsJSON),
			provider.DefaultModel,
			provider.Name,
			provider.Description,
		)
		if err == nil {
			updated++
		}
	}

	return updated
}

// withEmbeddedModels makes sure a registry never removes models this binary
// knows about. The registry (remote, or a cached copy that may be weeks old)
// replaces each provider's model list in the DB, so a registry older than the
// binary used to drop models that a migration had just added (e.g.
// claude-fable-5-5 vanished right after migration 52). When the registry's
// list lacks any embedded model, the embedded list wins (in its order) and
// models only the registry knows are appended.
func withEmbeddedModels(registry, embedded []Provider) []Provider {
	byID := make(map[string][]string, len(embedded))
	for _, p := range embedded {
		byID[p.ID] = p.Models
	}
	out := make([]Provider, len(registry))
	for i, p := range registry {
		out[i] = p
		emb, ok := byID[p.ID]
		if !ok {
			continue
		}
		have := make(map[string]bool, len(p.Models))
		for _, m := range p.Models {
			have[m] = true
		}
		stale := false
		for _, m := range emb {
			if !have[m] {
				stale = true
				break
			}
		}
		if !stale {
			continue
		}
		models := append([]string(nil), emb...)
		known := make(map[string]bool, len(emb))
		for _, m := range emb {
			known[m] = true
		}
		for _, m := range p.Models {
			if !known[m] {
				models = append(models, m)
			}
		}
		out[i].Models = models
	}
	return out
}
