package ops

import (
	"context"

	"github.com/wunderous/host-agents/internal/domain/oci"
)

// The oci domain owns these types and operations. HostOperationsService keeps
// delegating methods so the dispatch registry is unaffected; this file
// disappears with internal/ops itself.
type (
	EnsureOciBuilderArgs        = oci.EnsureOciBuilderArgs
	BuildAndPushOciImageArgs    = oci.BuildAndPushOciImageArgs
	InstallOCIRegistryArgs      = oci.InstallOCIRegistryArgs
	StageBuildContextArgs       = oci.StageBuildContextArgs
	ConfigureOciStorageArgs     = oci.ConfigureOciStorageArgs
	InspectContainerStorageArgs = oci.InspectContainerStorageArgs
	CleanupContainerStorageArgs = oci.CleanupContainerStorageArgs
)

func (s *HostOperationsService) oci() *oci.Service {
	s.ociOnce.Do(func() {
		s.ociSvc = oci.New(&s.shared, oci.Deps{
			KubernetesTargetURI: func(vmName string) (string, error) {
				return s.kubernetes().TargetURI(vmName)
			},
			ApplyManifest: func(uri, manifest string, onData func(string)) (map[string]any, error) {
				return s.kubernetes().ApplyManifest(ApplyManifestArgs{URI: uri, Manifest: manifest}, onData)
			},
			GetK8sResource: func(uri, kind, name, namespace string) (map[string]any, error) {
				return s.kubernetes().GetK8sResource(K8sResourceArgs{URI: uri, Kind: kind, ResourceName: name, Namespace: namespace})
			},
			DeleteK8sResource: func(uri, kind, name, namespace string, onData func(string)) (map[string]any, error) {
				return s.kubernetes().DeleteK8sResource(K8sResourceArgs{URI: uri, Kind: kind, ResourceName: name, Namespace: namespace}, onData)
			},
		}, s.ociStoragePolicyPath)
	})
	return s.ociSvc
}

func (s *HostOperationsService) EnsureOciBuilder(args EnsureOciBuilderArgs, onData func(string)) (map[string]any, error) {
	return s.oci().EnsureOciBuilder(args, onData)
}

func (s *HostOperationsService) BuildAndPushOciImage(ctx context.Context, args BuildAndPushOciImageArgs, onData func(string)) (map[string]any, error) {
	return s.oci().BuildAndPushOciImage(ctx, args, onData)
}

func (s *HostOperationsService) StageBuildContext(args StageBuildContextArgs, onData func(string)) (map[string]any, error) {
	return s.oci().StageBuildContext(args, onData)
}

func (s *HostOperationsService) InspectContainerStorage(ctx context.Context, args InspectContainerStorageArgs) (map[string]any, error) {
	return s.oci().InspectContainerStorage(ctx, args)
}

func (s *HostOperationsService) CleanupContainerStorage(ctx context.Context, args CleanupContainerStorageArgs, onData func(string)) (map[string]any, error) {
	return s.oci().CleanupContainerStorage(ctx, args, onData)
}

func (s *HostOperationsService) ConfigureOciStorage(ctx context.Context, args ConfigureOciStorageArgs, onData func(string)) (map[string]any, error) {
	return s.oci().ConfigureOciStorage(ctx, args, onData)
}

func (s *HostOperationsService) InstallOCIRegistry(args InstallOCIRegistryArgs, onData func(string)) (map[string]any, error) {
	return s.oci().InstallOCIRegistry(args, onData)
}

func (s *HostOperationsService) GetOCIRegistryStatus(args InstallOCIRegistryArgs) (map[string]any, error) {
	return s.oci().GetOCIRegistryStatus(args)
}

func (s *HostOperationsService) DeleteOCIRegistry(args InstallOCIRegistryArgs, onData func(string)) (map[string]any, error) {
	return s.oci().DeleteOCIRegistry(args, onData)
}
