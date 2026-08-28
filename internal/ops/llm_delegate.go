package ops

import (
	"context"

	"github.com/wunderous/host-agents/internal/domain/llm"
)

// The llm domain owns these types and operations. HostOperationsService keeps
// delegating methods so the dispatch registry and the domains that have not
// moved yet are unaffected; this file disappears with internal/ops itself.
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

func (s *HostOperationsService) llm() *llm.Service {
	s.llmOnce.Do(func() {
		s.llmSvc = llm.New(&s.shared, llm.Deps{
			KubernetesTargetURI: func(vmName string) (string, error) {
				return s.kubernetes().TargetURI(vmName)
			},
			ApplyManifest: func(uri, manifest string, onData func(string)) (map[string]any, error) {
				return s.kubernetes().ApplyManifest(ApplyManifestArgs{URI: uri, Manifest: manifest}, onData)
			},
			DeleteK8sResource: func(uri, kind, name, namespace string, onData func(string)) (map[string]any, error) {
				return s.kubernetes().DeleteK8sResource(K8sResourceArgs{URI: uri, Kind: kind, ResourceName: name, Namespace: namespace}, onData)
			},
		}, s.relayDirs[0], s.relayDirs[1])
	})
	return s.llmSvc
}

func (s *HostOperationsService) ProbeOllama(ctx context.Context, args ProbeOllamaArgs) (*LocalLLMProbeResult, error) {
	return s.llm().ProbeOllama(ctx, args)
}

func (s *HostOperationsService) ProbeLlamaServer(ctx context.Context, args ProbeLlamaServerArgs) (*LocalLLMProbeResult, error) {
	return s.llm().ProbeLlamaServer(ctx, args)
}

func (s *HostOperationsService) StartOllamaRuntime(ctx context.Context) (*LocalLLMProbeResult, error) {
	return s.llm().StartOllamaRuntime(ctx)
}

func (s *HostOperationsService) StopOllamaRuntime(ctx context.Context) error {
	return s.llm().StopOllamaRuntime(ctx)
}

func (s *HostOperationsService) StartLlamaServerRuntime(ctx context.Context) (*LocalLLMProbeResult, error) {
	return s.llm().StartLlamaServerRuntime(ctx)
}

func (s *HostOperationsService) StopLlamaServerRuntime(ctx context.Context) error {
	return s.llm().StopLlamaServerRuntime(ctx)
}

func (s *HostOperationsService) InstallOllamaModel(ctx context.Context, args InstallOllamaModelArgs) (*LocalLLMProbeResult, error) {
	return s.llm().InstallOllamaModel(ctx, args)
}

func (s *HostOperationsService) InstallLlamaServerModel(ctx context.Context, args InstallLlamaServerModelArgs) (*LocalLLMProbeResult, error) {
	return s.llm().InstallLlamaServerModel(ctx, args)
}

func (s *HostOperationsService) EnsureLlamaServerBinary(ctx context.Context, args BuildLlamaServerBinaryArgs, onData func(string)) (*LlamaServerBinaryBuildResult, error) {
	return s.llm().EnsureLlamaServerBinary(ctx, args, onData)
}

func (s *HostOperationsService) CheckOllamaPrerequisites() (*LocalLLMPrerequisitesResult, error) {
	return s.llm().CheckOllamaPrerequisites()
}

func (s *HostOperationsService) CheckLlamaServerPrerequisites() (*LocalLLMPrerequisitesResult, error) {
	return s.llm().CheckLlamaServerPrerequisites()
}

func (s *HostOperationsService) ConfigureOllamaModelContext(ctx context.Context, args ConfigureOllamaModelContextArgs) (*OllamaModelContextResult, error) {
	return s.llm().ConfigureOllamaModelContext(ctx, args)
}

func (s *HostOperationsService) GetOllamaModelContext(ctx context.Context, modelRef string) (*OllamaModelContextResult, error) {
	return s.llm().GetOllamaModelContext(ctx, modelRef)
}

func (s *HostOperationsService) EnsureLocalLLMRelay(ctx context.Context, args LocalLLMRelayArgs) (map[string]any, error) {
	return s.llm().EnsureLocalLLMRelay(ctx, args)
}

func (s *HostOperationsService) RemoveLocalLLMRelay(id string) (map[string]any, error) {
	return s.llm().RemoveLocalLLMRelay(id)
}

func (s *HostOperationsService) EnsureLocalLLMK3sProxy(args LocalLLMK3sProxyArgs, onData func(string)) (map[string]any, error) {
	return s.llm().EnsureLocalLLMK3sProxy(args, onData)
}

func (s *HostOperationsService) RemoveLocalLLMK3sProxy(vmName, namespace string) (map[string]any, error) {
	return s.llm().RemoveLocalLLMK3sProxy(vmName, namespace)
}

// DefaultOllamaModel is the model the dispatch layer falls back to.
const DefaultOllamaModel = llm.DefaultOllamaModel

func (s *HostOperationsService) ProbeOpenAICompatibleServer(ctx context.Context, args ProbeOpenAICompatibleArgs) (*RuntimeObservation, error) {
	return s.llm().ProbeOpenAICompatibleServer(ctx, args)
}
