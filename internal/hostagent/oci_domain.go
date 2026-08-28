package hostagent

import (
	"github.com/wunderous/host-agents/internal/domain/oci"
)

// These aliases name the oci domain's types where the dispatch layer still
// spells them here. The operations live on the domain itself.
type (
	EnsureOciBuilderArgs        = oci.EnsureOciBuilderArgs
	BuildAndPushOciImageArgs    = oci.BuildAndPushOciImageArgs
	InstallOCIRegistryArgs      = oci.InstallOCIRegistryArgs
	StageBuildContextArgs       = oci.StageBuildContextArgs
	ConfigureOciStorageArgs     = oci.ConfigureOciStorageArgs
	InspectContainerStorageArgs = oci.InspectContainerStorageArgs
	CleanupContainerStorageArgs = oci.CleanupContainerStorageArgs
)

func (s *Service) Oci() *oci.Service {
	s.ociOnce.Do(func() {
		s.ociSvc = oci.New(&s.shared, oci.Deps{
			KubernetesTargetURI: func(vmName string) (string, error) {
				return s.Kubernetes().TargetURI(vmName)
			},
			ApplyManifest: func(uri, manifest string, onData func(string)) (map[string]any, error) {
				return s.Kubernetes().ApplyManifest(ApplyManifestArgs{URI: uri, Manifest: manifest}, onData)
			},
			GetK8sResource: func(uri, kind, name, namespace string) (map[string]any, error) {
				return s.Kubernetes().GetK8sResource(K8sResourceArgs{URI: uri, Kind: kind, ResourceName: name, Namespace: namespace})
			},
			DeleteK8sResource: func(uri, kind, name, namespace string, onData func(string)) (map[string]any, error) {
				return s.Kubernetes().DeleteK8sResource(K8sResourceArgs{URI: uri, Kind: kind, ResourceName: name, Namespace: namespace}, onData)
			},
		}, s.ociStoragePolicyPath)
	})
	return s.ociSvc
}
