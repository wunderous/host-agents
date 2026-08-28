package hostagent

import (
	"github.com/wunderous/host-agents/internal/domain/llm"
)

// These aliases name the llm domain's types where the dispatch layer still
// spells them here. The operations live on the domain itself.
type (
	ProbeOpenAICompatibleArgs       = llm.ProbeOpenAICompatibleArgs
	RuntimeObservation              = llm.RuntimeObservation
	RuntimeModel                    = llm.RuntimeModel
	LocalLLMProbeResult             = llm.LocalLLMProbeResult
	LocalLLMPrerequisitesResult     = llm.LocalLLMPrerequisitesResult
	LocalLLMRelayArgs               = llm.LocalLLMRelayArgs
	LocalLLMK3sProxyArgs            = llm.LocalLLMK3sProxyArgs
	ProbeOllamaArgs                 = llm.ProbeOllamaArgs
	ProbeLlamaServerArgs            = llm.ProbeLlamaServerArgs
	InstallOllamaModelArgs          = llm.InstallOllamaModelArgs
	InstallLlamaServerModelArgs     = llm.InstallLlamaServerModelArgs
	BuildLlamaServerBinaryArgs      = llm.BuildLlamaServerBinaryArgs
	LlamaServerBinaryBuildResult    = llm.LlamaServerBinaryBuildResult
	ConfigureOllamaModelContextArgs = llm.ConfigureOllamaModelContextArgs
	OllamaModelContextResult        = llm.OllamaModelContextResult
)

// ValidateLocalLLMRelayArgs is pure validation with no service state.
func ValidateLocalLLMRelayArgs(args LocalLLMRelayArgs) error {
	return llm.ValidateLocalLLMRelayArgs(args)
}

// RenderLocalLLMK3sProxyManifest and its secret-bearing form are pure renderers.
func RenderLocalLLMK3sProxyManifest(args LocalLLMK3sProxyArgs) (string, error) {
	return llm.RenderLocalLLMK3sProxyManifest(args)
}

func RenderLocalLLMK3sProxyManifestWithSecrets(args LocalLLMK3sProxyArgs) (string, error) {
	return llm.RenderLocalLLMK3sProxyManifestWithSecrets(args)
}

func (s *Service) Llm() *llm.Service {
	s.llmOnce.Do(func() {
		s.llmSvc = llm.New(&s.shared, llm.Deps{
			KubernetesTargetURI: func(vmName string) (string, error) {
				return s.Kubernetes().TargetURI(vmName)
			},
			ApplyManifest: func(uri, manifest string, onData func(string)) (map[string]any, error) {
				return s.Kubernetes().ApplyManifest(ApplyManifestArgs{URI: uri, Manifest: manifest}, onData)
			},
			DeleteK8sResource: func(uri, kind, name, namespace string, onData func(string)) (map[string]any, error) {
				return s.Kubernetes().DeleteK8sResource(K8sResourceArgs{URI: uri, Kind: kind, ResourceName: name, Namespace: namespace}, onData)
			},
		}, s.relayDirs[0], s.relayDirs[1])
	})
	return s.llmSvc
}

// DefaultOllamaModel is the model the dispatch layer falls back to.
const DefaultOllamaModel = llm.DefaultOllamaModel
