package hostmcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wunderous/host-agents/contracts/capability"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	"github.com/wunderous/host-agents/internal/cordis"
	provideradapter "github.com/wunderous/host-agents/internal/cordis/mcp"
	"github.com/wunderous/host-agents/internal/recipe"
	"github.com/wunderous/host-agents/internal/state"
	"github.com/wunderous/host-agents/internal/tools"
)

func (s *Server) handleProviderInstall(args map[string]any) (*mcp.CallToolResult, error) {
	descriptor, baseDir, err := loadProviderDescriptor(args)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	endpoint := descriptor.Server.Endpoint
	if override := recipeStringField(args, "endpoint"); override != "" {
		descriptor.Server.Endpoint = override
		endpoint = override
	}
	adapter, err := provideradapter.Connect(context.Background(), descriptor, provideradapter.Options{BearerToken: recipeStringField(args, "token")})
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	manifest, err := adapter.InstallManifest(context.Background())
	if err != nil {
		_ = adapter.Close()
		return tools.ErrorResult(err), nil
	}
	manifestHash, err := hashProviderValue(manifest)
	if err != nil {
		_ = adapter.Close()
		return tools.ErrorResult(err), nil
	}
	candidate, err := s.providerLifecycle.CreateCandidate(manifest.Provider, manifestHash, endpoint, s.CatalogSnapshot().Revision)
	if err != nil {
		_ = adapter.Close()
		return tools.ErrorResult(err), nil
	}
	if err := s.persistProviderGeneration(candidate); err != nil {
		_ = s.providerLifecycle.Fail(candidate.ID, err.Error())
		_ = adapter.Close()
		return tools.ErrorResult(fmt.Errorf("persist provider generation: %w", err)), nil
	}
	s.emitProviderLifecycleEvent(context.Background(), ProviderEventCandidate, manifest.Provider.ID, candidate.ID, "")
	s.providerMu.Lock()
	s.providerCandidates[candidate.ID] = adapter
	s.providerCandidateManifests[candidate.ID] = manifest
	s.providerMu.Unlock()

	recipeRef, err := selectProviderRecipe(manifest, recipeStringField(args, "mode"))
	if err != nil {
		_ = s.providerLifecycle.Fail(candidate.ID, err.Error())
		_ = adapter.Close()
		s.cleanupProviderCandidate(candidate.ID, manifest.Provider.ID)
		return tools.ErrorResult(err), nil
	}
	source := recipeStringField(args, "recipeSource")
	if source == "" {
		source = recipeRef.Source.URI
		if baseDir != "" && !filepath.IsAbs(source) && !strings.HasPrefix(source, "github:") && !strings.HasPrefix(source, "https://") {
			source = filepath.Join(baseDir, source)
		}
	}
	inputValues, err := recipeInputValues(args)
	if err != nil {
		_ = s.providerLifecycle.Fail(candidate.ID, err.Error())
		_ = adapter.Close()
		s.cleanupProviderCandidate(candidate.ID, manifest.Provider.ID)
		return tools.ErrorResult(err), nil
	}
	runArgs := map[string]any{
		"source":               source,
		"revision":             firstNonEmpty(recipeStringField(args, "revision"), recipeRef.Source.Revision),
		"sha256":               firstNonEmpty(recipeStringField(args, "sha256"), recipeRef.Source.SHA256),
		"inputs":               inputValues,
		"activate":             recipeBoolField(args, "activate"),
		"resume":               recipeBoolField(args, "resume"),
		"providerId":           manifest.Provider.ID,
		"providerVersion":      manifest.Provider.Version,
		"providerGenerationId": candidate.ID,
		"providerManifest":     manifest,
	}
	if manifest.Validation.Capability == capability.Tunneling {
		return s.handleRunTunnelRecipe(runArgs)
	}
	return s.handleRunRuntimeRecipe(runArgs)
}

func (s *Server) handleProviderValidate(args map[string]any) (*mcp.CallToolResult, error) {
	providerID := recipeStringField(args, "provider")
	session, err := s.providerLifecycle.OpenSession(providerID)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	defer session.Close()
	s.providerMu.RLock()
	adapter := s.providerGenerationAdapter(providerID, session.GenerationID())
	validationOperation := s.providerValidation[providerID]
	s.providerMu.RUnlock()
	if adapter == nil {
		return tools.ErrorResult(fmt.Errorf("provider %q is not connected", providerID)), nil
	}
	operation := recipeStringField(args, "operation")
	if operation == "" {
		operation = validationOperation
	}
	if operation == "" {
		return tools.ErrorResult(fmt.Errorf("provider %q does not declare a validation operation", providerID)), nil
	}
	result, err := adapter.CallSynchronousOnly(context.Background(), operation, args)
	if err != nil {
		return tools.ErrorResult(err), nil
	}
	return result, nil
}

func (s *Server) handleProviderStatus(args map[string]any) (*mcp.CallToolResult, error) {
	providerID := recipeStringField(args, "provider")
	active, activeOK := s.providerLifecycle.Active(providerID)
	connected := false
	if activeOK {
		connected = s.providerGenerationAdapter(providerID, active.ID) != nil
	}
	result := map[string]any{"providerId": providerID, "connected": connected, "active": activeOK}
	if activeOK {
		result["generation"] = active
	}
	return structuredResult(result, ""), nil
}

func (s *Server) cleanupProviderCandidate(generationID, _ string) {
	_ = s.providerLifecycle.Fail(generationID, "provider setup failed")
	s.providerMu.Lock()
	adapter := s.providerCandidates[generationID]
	delete(s.providerCandidates, generationID)
	delete(s.providerCandidateManifests, generationID)
	s.providerMu.Unlock()
	if adapter != nil {
		_ = adapter.Close()
	}
	if generation, ok := s.providerLifecycle.Get(generationID); ok {
		_ = s.persistProviderGeneration(generation)
	}
}

func (s *Server) completeProviderCandidate(generationID string) {
	s.providerMu.Lock()
	delete(s.providerCandidates, generationID)
	delete(s.providerCandidateManifests, generationID)
	s.providerMu.Unlock()
}

func (s *Server) handleProviderReload(args map[string]any) (*mcp.CallToolResult, error) {
	if recipeStringField(args, "source") == "" && recipeStringField(args, "descriptor") == "" {
		return tools.ErrorResult(fmt.Errorf("provider reload requires descriptor source")), nil
	}
	return s.handleProviderInstall(args)
}

func loadProviderDescriptor(args map[string]any) (providercontract.PluginDescriptor, string, error) {
	var raw []byte
	baseDir := ""
	if value, ok := args["descriptor"]; ok && value != nil {
		var err error
		raw, err = json.Marshal(value)
		if err != nil {
			return providercontract.PluginDescriptor{}, "", fmt.Errorf("encode provider descriptor: %w", err)
		}
	} else {
		source := firstNonEmpty(recipeStringField(args, "source"), recipeStringField(args, "descriptorSource"))
		if source == "" {
			return providercontract.PluginDescriptor{}, "", fmt.Errorf("provider descriptor source is required")
		}
		if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "github:") {
			return providercontract.PluginDescriptor{}, "", fmt.Errorf("provider descriptors must be trusted local composition data")
		}
		path := filepath.Clean(source)
		data, err := os.ReadFile(path)
		if err != nil {
			return providercontract.PluginDescriptor{}, "", fmt.Errorf("read provider descriptor: %w", err)
		}
		raw = data
		baseDir = filepath.Dir(path)
	}
	var descriptor providercontract.PluginDescriptor
	if err := recipe.Decode(raw, &descriptor, "provider descriptor"); err != nil {
		return providercontract.PluginDescriptor{}, "", err
	}
	if err := providercontract.ValidateDescriptor(descriptor); err != nil {
		return providercontract.PluginDescriptor{}, "", err
	}
	return descriptor, baseDir, nil
}

func selectProviderRecipe(manifest providercontract.InstallManifest, mode string) (providercontract.RecipeRef, error) {
	for _, recipeRef := range manifest.Recipes {
		if mode == "" || recipeRef.Mode == mode {
			return recipeRef, nil
		}
	}
	return providercontract.RecipeRef{}, fmt.Errorf("provider manifest has no recipe for mode %q", mode)
}

func hashProviderValue(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (s *Server) persistProviderGeneration(g cordis.ProviderGeneration) error {
	if s.state == nil {
		return nil
	}
	var descriptorJSON, manifestJSON string
	s.providerMu.RLock()
	manifest, manifestOK := s.providerManifests[g.Provider.ID]
	if !manifestOK {
		for _, candidate := range s.providerCandidateManifests {
			if candidate.Provider == g.Provider {
				manifest = candidate
				manifestOK = true
				break
			}
		}
	}
	s.providerMu.RUnlock()
	if manifestOK {
		// The descriptor is reconstructed from the validated manifest for
		// restart recovery. Endpoint and capability identity are the only
		// trusted inputs required to reconnect; executable/args remain launch
		// metadata owned by the process supervisor.
		descriptor := providercontract.PluginDescriptor{
			Schema:       providercontract.PluginDescriptorVersion,
			PluginID:     g.Provider.ID,
			Version:      g.Provider.Version,
			Capabilities: append([]providercontract.CapabilityRef(nil), manifest.Provides...),
			Server: providercontract.ServerDescriptor{
				Transport: "streamable_http",
				Endpoint:  g.Endpoint,
			},
		}
		if encoded, err := json.Marshal(descriptor); err == nil {
			descriptorJSON = string(encoded)
		}
		if encoded, err := json.Marshal(manifest); err == nil {
			manifestJSON = string(encoded)
		}
	}
	return s.state.SaveProviderGeneration(state.ProviderGenerationRecord{
		GenerationID:    g.ID,
		ProviderID:      g.Provider.ID,
		ProviderVersion: g.Provider.Version,
		ManifestHash:    g.ManifestHash,
		Endpoint:        g.Endpoint,
		DescriptorJSON:  descriptorJSON,
		ManifestJSON:    manifestJSON,
		CatalogRevision: g.CatalogRevision,
		Status:          string(g.State),
		CreatedAt:       g.CreatedAt.UTC().Format(time.RFC3339Nano),
		ActiveAt:        formatProviderTime(g.ActiveAt),
	})
}

func formatProviderTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (s *Server) restoreProviderGenerations() error {
	if s.state == nil {
		return nil
	}
	records, err := s.state.ListProviderGenerations()
	if err != nil {
		return err
	}
	for _, record := range records {
		createdAt, parseErr := time.Parse(time.RFC3339Nano, record.CreatedAt)
		if parseErr != nil {
			return fmt.Errorf("parse provider generation %q creation time: %w", record.GenerationID, parseErr)
		}
		generation := cordis.ProviderGeneration{
			ID:              record.GenerationID,
			Provider:        providercontract.ProviderRef{ID: record.ProviderID, Version: record.ProviderVersion},
			ManifestHash:    record.ManifestHash,
			Endpoint:        record.Endpoint,
			CatalogRevision: record.CatalogRevision,
			State:           cordis.GenerationState(record.Status),
			CreatedAt:       createdAt,
		}
		if record.ActiveAt != "" {
			generation.ActiveAt, parseErr = time.Parse(time.RFC3339Nano, record.ActiveAt)
			if parseErr != nil {
				return fmt.Errorf("parse provider generation %q activation time: %w", record.GenerationID, parseErr)
			}
		}
		if err := s.providerLifecycle.Restore(generation); err != nil {
			return err
		}
	}
	for _, record := range records {
		if record.Status != string(cordis.GenerationActive) || record.DescriptorJSON == "" || record.ManifestJSON == "" {
			continue
		}
		var descriptor providercontract.PluginDescriptor
		if err := json.Unmarshal([]byte(record.DescriptorJSON), &descriptor); err != nil {
			continue
		}
		var manifest providercontract.InstallManifest
		if err := json.Unmarshal([]byte(record.ManifestJSON), &manifest); err != nil {
			continue
		}
		if err := providercontract.ValidateDescriptor(descriptor); err != nil {
			continue
		}
		if err := providercontract.ValidateInstallManifest(manifest, providercontract.ProviderRef{ID: descriptor.PluginID, Version: descriptor.Version}); err != nil {
			continue
		}
		manifestHash, err := hashProviderValue(manifest)
		if err != nil || manifestHash != record.ManifestHash {
			continue
		}
		adapter, err := provideradapter.Connect(context.Background(), descriptor, provideradapter.Options{})
		if err != nil {
			// The durable generation remains active and retryable; the provider
			// may be started after the Host Agent during a supervisor restart.
			continue
		}
		if err := s.mountProviderGeneration(manifest, record.GenerationID, adapter); err != nil {
			_ = adapter.Close()
			continue
		}
		s.providerMu.Lock()
		s.providerValidation[record.ProviderID] = manifest.Validation.Operation
		s.providerManifests[record.ProviderID] = manifest
		s.providerMu.Unlock()
		if err := s.registerProviderServices(manifest); err != nil {
			_ = s.unmountProviderGeneration(record.ProviderID, record.GenerationID)
			_ = adapter.Close()
			s.providerMu.Lock()
			delete(s.providerValidation, record.ProviderID)
			delete(s.providerManifests, record.ProviderID)
			s.providerMu.Unlock()
		}
	}
	return nil
}
