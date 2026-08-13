package ops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// CheckLlamaServerPrerequisites is the production capability probe. It is
// reports only the llama-server capability and never probes an alternate
// serving runtime.
func (s *HostOperationsService) CheckLlamaServerPrerequisites() (*LocalLLMPrerequisitesResult, error) {
	cfg := loadLlamaServerConfig()
	result := &LocalLLMPrerequisitesResult{
		Supported:            runtime.GOOS == "linux",
		SystemdUserAvailable: false,
		Architecture:         runtime.GOARCH,
		CPUCount:             runtime.NumCPU(),
		ModelsDirectory:      filepath.Dir(cfg.ArtifactPath),
	}
	if _, err := s.hostCommandRunner([]string{"systemctl", "--user", "show-environment"}, nil, 10*time.Second); err == nil {
		result.SystemdUserAvailable = true
	}
	if _, err := os.Stat(cfg.BinaryPath); err == nil {
		result.LlamaServerBinaryPresent = true
	}
	result.LlamaServerCudaEnabled = cfg.CudaEnabled
	result.LlamaServerBinarySource = cfg.BinarySource
	result.LlamaServerBuildRevision = cfg.SourceRevision
	result.LlamaServerBinarySHA256 = cfg.BinarySHA256
	if res, err := s.hostCommandRunner([]string{"systemctl", "--user", "is-active", "opute-llama-server.service"}, nil, 5*time.Second); err == nil {
		result.LlamaServerServiceActive = strings.TrimSpace(res.Stdout) == "active"
	}
	if res, err := s.hostCommandRunner(append(nvidiaSmiCommand(), "--query-gpu=name,memory.total", "--format=csv,noheader,nounits"), nil, 5*time.Second); err == nil && res.ExitCode == 0 {
		line := strings.TrimSpace(strings.Split(res.Stdout, "\n")[0])
		parts := strings.Split(line, ",")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			result.GPU = strings.TrimSpace(parts[0])
			result.NvidiaSmiOk = true
		}
		if len(parts) > 1 {
			if mb, parseErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64); parseErr == nil {
				result.GpuMemoryTotalBytes = mb * 1024 * 1024
			}
		}
	}
	result.CudaLibraryPresent = cudaUserLibraryPresent()
	result.CMakePresent = hostCommandAvailable(s, "cmake")
	result.NinjaPresent = hostCommandAvailable(s, "ninja")
	result.CudaCompilerPresent = hostCommandAvailable(s, "nvcc")
	result.BuildToolchainReady = result.CMakePresent && result.NinjaPresent && result.CudaCompilerPresent
	if result.LlamaServerServiceActive {
		if probe, err := s.ProbeLlamaServer(context.Background(), ProbeLlamaServerArgs{}); err == nil && probe != nil {
			result.RuntimeGpuAccelerated = probe.GpuAccelerated
			result.RuntimeSizeVramBytes = probe.SizeVramBytes
			result.RuntimeLoadedModel = probe.LoadedModel
		}
	}
	result.ReadyForGpuInference = result.NvidiaSmiOk && result.CudaLibraryPresent && result.LlamaServerCudaEnabled && result.LlamaServerBinarySource == "host-build"
	result.ReadyForInstall = result.Supported && result.SystemdUserAvailable && result.LlamaServerBinaryPresent && result.ReadyForGpuInference
	if !result.Supported {
		result.Blockers = append(result.Blockers, "llama-server requires Linux or WSL2")
	}
	if !result.SystemdUserAvailable {
		result.Blockers = append(result.Blockers, "systemd --user is unavailable")
	}
	if !result.LlamaServerBinaryPresent {
		result.Blockers = append(result.Blockers, "pinned llama-server binary is not installed")
	}
	if !result.NvidiaSmiOk || !result.CudaLibraryPresent {
		result.Blockers = append(result.Blockers, "CUDA GPU prerequisites are unavailable; CPU fallback is rejected")
	}
	if !result.LlamaServerCudaEnabled || result.LlamaServerBinarySource != "host-build" {
		result.Blockers = append(result.Blockers, "llama-server must be built and verified with CUDA by the host agent")
	}
	if !result.BuildToolchainReady {
		missing := make([]string, 0, 3)
		if !result.CMakePresent {
			missing = append(missing, "cmake")
		}
		if !result.NinjaPresent {
			missing = append(missing, "ninja")
		}
		if !result.CudaCompilerPresent {
			missing = append(missing, "nvcc/CUDA toolkit")
		}
		result.Blockers = append(result.Blockers, "CUDA llama-server build toolchain is incomplete: "+strings.Join(missing, ", "))
	}
	return result, nil
}

func hostCommandAvailable(s *HostOperationsService, name string) bool {
	result, err := s.hostCommandRunner([]string{"bash", "-lc", "command -v " + llamaShellQuote(name)}, nil, 5*time.Second)
	return err == nil && result.ExitCode == 0 && strings.TrimSpace(result.Stdout) != ""
}
