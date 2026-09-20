package kubernetes

import (
	"errors"
	"strings"
)

type GuestStorageArgs struct {
	URI               string   `json:"uri,omitempty"`
	IncludeRegistry   bool     `json:"includeRegistry,omitempty"`
	DryRun            bool     `json:"dryRun,omitempty"`
	MinAgeSeconds     *int64   `json:"minAgeSeconds,omitempty"`
	ExtraKeepTags     []string `json:"extraKeepTags,omitempty"`
	RegistryNamespace string   `json:"registryNamespace,omitempty"`
	RegistryName      string   `json:"registryName,omitempty"`
}

func (s *Service) InspectGuestStorage(args GuestStorageArgs) (map[string]any, error) {
	return s.delegateGuestStorage(KubernetesInspectGuestStorageOperation, args, false)
}

func (s *Service) PruneUnusedClusterImages(args GuestStorageArgs) (map[string]any, error) {
	return s.delegateGuestStorage(KubernetesPruneUnusedImagesOperation, args, true)
}

func (s *Service) GarbageCollectClusterRegistry(args GuestStorageArgs) (map[string]any, error) {
	return s.delegateGuestStorage(KubernetesGarbageCollectRegistryOperation, args, true)
}

func (s *Service) TrimGuestStorage(args GuestStorageArgs) (map[string]any, error) {
	return s.delegateGuestStorage(KubernetesTrimGuestStorageOperation, args, false)
}

func (s *Service) delegateGuestStorage(operation string, args GuestStorageArgs, includeDryRun bool) (map[string]any, error) {
	if s.executor == nil {
		return nil, errors.New("kubernetes provider is required for guest storage operations")
	}
	uri := strings.TrimSpace(args.URI)
	if uri == "" {
		return nil, errors.New("uri is required")
	}
	payload := map[string]any{
		"includeRegistry":   args.IncludeRegistry,
		"extraKeepTags":     stringsToAny(args.ExtraKeepTags),
		"registryNamespace": strings.TrimSpace(args.RegistryNamespace),
		"registryName":      strings.TrimSpace(args.RegistryName),
	}
	if includeDryRun {
		payload["dryRun"] = args.DryRun
	}
	if args.MinAgeSeconds != nil {
		payload["minAgeSeconds"] = *args.MinAgeSeconds
	}
	out, delegated, err := s.ExecuteProvider(operation, uri, payload)
	if !delegated {
		return nil, errors.New("kubernetes provider is required for guest storage operations")
	}
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	out["uri"] = uri
	if _, ok := out["clusterId"]; !ok {
		out["clusterId"] = clusterIDFromURI(uri)
	}
	return out, nil
}
