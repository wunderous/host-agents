package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/textutil"
)

// BuildLlamaServerBinaryArgs describes the only supported production binary
// source: a pinned llama.cpp source archive compiled with CUDA by the host
// agent. The control plane never supplies an arbitrary executable path.
type BuildLlamaServerBinaryArgs struct {
	SourceURI         string
	SourceSHA256      string
	SourceRevision    string
	OutputPath        string
	CudaArchitectures string
}

type LlamaServerBinaryBuildResult struct {
	Runtime           string `json:"runtime"`
	BinaryPath        string `json:"binaryPath"`
	BinaryVersion     string `json:"binaryVersion"`
	BinarySHA256      string `json:"binarySha256"`
	BinarySource      string `json:"binarySource"`
	SourceURI         string `json:"sourceUri"`
	SourceSHA256      string `json:"sourceSha256"`
	SourceRevision    string `json:"sourceRevision"`
	CudaEnabled       bool   `json:"cudaEnabled"`
	CudaArchitectures string `json:"cudaArchitectures,omitempty"`
	BuildToolchain    string `json:"buildToolchain"`
	RuntimeBuild      string `json:"runtimeBuild"`
}

var llamaSourceRevisionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{4,128}$`)
var llamaCudaArchitecturePattern = regexp.MustCompile(`^[0-9;]+$`)

func llamaShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func managedLlamaPath(home, requested, defaultPath string) (string, error) {
	path := strings.TrimSpace(requested)
	if path == "" {
		path = defaultPath
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("llama-server path must be absolute")
	}
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("llama-server path must remain under the host-agent managed home directory")
	}
	return path, nil
}

// EnsureLlamaServerBinary downloads, verifies, builds, and verifies a CUDA
// llama-server binary. All side effects remain inside host-agent-managed
// directories and are observable through the MCP operation stream.
func (s *Service) EnsureLlamaServerBinary(ctx context.Context, args BuildLlamaServerBinaryArgs, onData func(string)) (*LlamaServerBinaryBuildResult, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("CUDA llama-server builds require a Linux host")
	}
	if !strings.HasPrefix(strings.TrimSpace(args.SourceURI), "https://") {
		return nil, fmt.Errorf("sourceUri must use HTTPS")
	}
	if !isSHA256(args.SourceSHA256) {
		return nil, fmt.Errorf("sourceSha256 must be a SHA-256 hex digest")
	}
	if !llamaSourceRevisionPattern.MatchString(strings.TrimSpace(args.SourceRevision)) {
		return nil, fmt.Errorf("sourceRevision contains invalid characters")
	}
	if arch := strings.TrimSpace(args.CudaArchitectures); arch != "" && !llamaCudaArchitecturePattern.MatchString(arch) {
		return nil, fmt.Errorf("cudaArchitectures must contain only numeric architectures separated by semicolons")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	managedRoot := filepath.Join(home, ".local", "share", "opute", "llama-server")
	outputPath, err := managedLlamaPath(home, args.OutputPath, filepath.Join(managedRoot, "llama-server"))
	if err != nil {
		return nil, err
	}
	if adopted, ok := adoptExistingLlamaServerBinary(outputPath, args); ok {
		return adopted, nil
	}
	cacheRoot := filepath.Join(home, ".cache", "opute", "llama-server", strings.TrimSpace(args.SourceRevision))
	archivePath := filepath.Join(cacheRoot, "llama.cpp.tar.gz")
	sourceRoot := filepath.Join(cacheRoot, "source")
	buildRoot := filepath.Join(cacheRoot, "build")
	for _, path := range []string{archivePath, sourceRoot, buildRoot, outputPath} {
		if _, err := managedLlamaPath(home, path, path); err != nil {
			return nil, err
		}
	}
	missingTools := make([]string, 0, 3)
	for _, tool := range []string{"cmake", "ninja", "nvcc"} {
		check, checkErr := s.shared.HostCommandRunnerContext(ctx, []string{"bash", "-lc", "command -v " + llamaShellQuote(tool)}, nil, 5*time.Second)
		if checkErr != nil || check.ExitCode != 0 || strings.TrimSpace(check.Stdout) == "" {
			missingTools = append(missingTools, tool)
		}
	}
	if len(missingTools) > 0 {
		return nil, fmt.Errorf("CUDA llama-server build toolchain is incomplete; install or provision: %s", strings.Join(missingTools, ", "))
	}

	if _, statErr := os.Stat(archivePath); statErr != nil {
		if !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("inspect cached llama.cpp source archive: %w", statErr)
		}
		if err := downloadVerifiedLlamaFile(ctx, args.SourceURI, archivePath, args.SourceSHA256, false); err != nil {
			return nil, err
		}
	} else if err := verifyLlamaArtifact(archivePath, args.SourceSHA256); err != nil {
		return nil, fmt.Errorf("cached llama.cpp source archive verification failed: %w", err)
	}

	archFlag := ""
	if arch := strings.TrimSpace(args.CudaArchitectures); arch != "" {
		archFlag = " -DCMAKE_CUDA_ARCHITECTURES=" + arch
	}
	buildScript := fmt.Sprintf(`set -euo pipefail
# Keep the revision-scoped CMake cache and objects across MCP reconnects. A
# caller timeout must not turn a resumable host build into another full CUDA
# compilation; the verified archive and revision are the cache identity.
if [ ! -f %s/CMakeCache.txt ]; then
  rm -rf %s %s
  mkdir -p %s %s
  tar -xzf %s -C %s --strip-components=1
  cmake -S %s -B %s -G Ninja -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_CUDA=ON -DLLAMA_CURL=OFF%s
fi
cmake --build %s --target llama-server --parallel 4
test -x %s/bin/llama-server
mkdir -p %s
install -m 0755 %s/bin/llama-server %s
`,
		llamaShellQuote(buildRoot), llamaShellQuote(sourceRoot), llamaShellQuote(buildRoot), llamaShellQuote(sourceRoot), llamaShellQuote(buildRoot),
		llamaShellQuote(archivePath), llamaShellQuote(sourceRoot), llamaShellQuote(sourceRoot), llamaShellQuote(buildRoot), archFlag,
		llamaShellQuote(buildRoot), llamaShellQuote(buildRoot), llamaShellQuote(filepath.Dir(outputPath)), llamaShellQuote(buildRoot), llamaShellQuote(outputPath))
	buildResult, buildErr := s.shared.HostWorkloadRunnerContext(ctx, "llama-server-build-"+outputPath, []string{"bash", "-lc", buildScript}, onData, 45*time.Minute)
	if buildErr != nil {
		return nil, fmt.Errorf("build CUDA llama-server: %w", buildErr)
	}
	if buildResult.ExitCode != 0 {
		message := strings.TrimSpace(textutil.FirstNonEmpty(buildResult.Stderr, buildResult.Stdout))
		if message == "" {
			message = fmt.Sprintf("exit code %d", buildResult.ExitCode)
		}
		return nil, fmt.Errorf("build CUDA llama-server: %s", message)
	}

	versionResult, err := s.shared.HostCommandRunnerContext(ctx, []string{outputPath, "--version"}, onData, 30*time.Second)
	if err != nil || versionResult.ExitCode != 0 {
		if err != nil {
			return nil, fmt.Errorf("verify llama-server binary version: %w", err)
		}
		message := strings.TrimSpace(textutil.FirstNonEmpty(versionResult.Stderr, versionResult.Stdout))
		if message == "" {
			message = fmt.Sprintf("exit code %d", versionResult.ExitCode)
		}
		return nil, fmt.Errorf("verify llama-server binary version: %s", message)
	}
	// llama.cpp has emitted a successful --version response on stderr in some
	// builds. Treat either stream as the version output; exit status remains the
	// authoritative failure signal above.
	version := strings.TrimSpace(textutil.FirstNonEmpty(versionResult.Stdout, versionResult.Stderr))
	if version == "" {
		return nil, fmt.Errorf("llama-server binary returned an empty version")
	}

	cudaEnabled, err := s.verifyLlamaServerCudaLinkage(ctx, outputPath)
	if err != nil {
		return nil, err
	}
	if !cudaEnabled {
		return nil, fmt.Errorf("built llama-server does not contain verifiable CUDA linkage")
	}
	binarySHA, err := fileSHA256(outputPath)
	if err != nil {
		return nil, fmt.Errorf("hash built llama-server binary: %w", err)
	}

	return &LlamaServerBinaryBuildResult{
		Runtime:           "llama-cpp",
		BinaryPath:        outputPath,
		BinaryVersion:     version,
		BinarySHA256:      binarySHA,
		BinarySource:      "host-build",
		SourceURI:         strings.TrimSpace(args.SourceURI),
		SourceSHA256:      strings.ToLower(strings.TrimSpace(args.SourceSHA256)),
		SourceRevision:    strings.TrimSpace(args.SourceRevision),
		CudaEnabled:       true,
		CudaArchitectures: strings.TrimSpace(args.CudaArchitectures),
		BuildToolchain:    "cmake+ninja+cuda",
		RuntimeBuild:      fmt.Sprintf("llama.cpp:%s:cuda", strings.TrimSpace(args.SourceRevision)),
	}, nil
}

// verifyLlamaServerCudaLinkage is the host-owned source of truth for the
// CUDA capability bit. Build results and control-plane payloads are metadata;
// the managed executable must itself contain CUDA linkage before it can be
// used for production serving.
func (s *Service) verifyLlamaServerCudaLinkage(ctx context.Context, path string) (bool, error) {
	linkResult, err := s.shared.HostCommandRunnerContext(ctx, []string{"bash", "-lc", fmt.Sprintf("set -o pipefail; { ldd %s 2>/dev/null || true; strings %s 2>/dev/null || true; }", llamaShellQuote(path), llamaShellQuote(path))}, nil, 30*time.Second)
	if err != nil {
		return false, fmt.Errorf("inspect llama-server CUDA linkage: %w", err)
	}
	linkage := strings.ToLower(linkResult.Stdout + "\n" + linkResult.Stderr)
	return strings.Contains(linkage, "ggml-cuda") || strings.Contains(linkage, "cudart") || strings.Contains(linkage, "cublas"), nil
}

// adoptExistingLlamaServerBinary makes the ensure operation idempotent after
// install_local_llm_model has persisted the verified binary manifest.  A
// reconcile must not trigger another CUDA rebuild merely because the control
// plane asked for the same pinned source revision again.
func adoptExistingLlamaServerBinary(outputPath string, args BuildLlamaServerBinaryArgs) (*LlamaServerBinaryBuildResult, bool) {
	cfg := loadLlamaServerConfig()
	if cfg.BinarySource != "host-build" || filepath.Clean(cfg.BinaryPath) != filepath.Clean(outputPath) {
		return nil, false
	}
	if cfg.SourceRevision != strings.TrimSpace(args.SourceRevision) ||
		cfg.SourceSHA256 != strings.ToLower(strings.TrimSpace(args.SourceSHA256)) ||
		(strings.TrimSpace(args.CudaArchitectures) != "" && cfg.CudaArchitectures != strings.TrimSpace(args.CudaArchitectures)) {
		return nil, false
	}
	if cfg.BinaryVersion == "" || !cfg.CudaEnabled || cfg.BinarySHA256 == "" {
		return nil, false
	}
	if _, err := os.Stat(outputPath); err != nil {
		return nil, false
	}
	actual, err := fileSHA256(outputPath)
	if err != nil || !strings.EqualFold(actual, cfg.BinarySHA256) {
		return nil, false
	}
	return &LlamaServerBinaryBuildResult{
		Runtime:           "llama-cpp",
		BinaryPath:        outputPath,
		BinaryVersion:     cfg.BinaryVersion,
		BinarySHA256:      cfg.BinarySHA256,
		BinarySource:      cfg.BinarySource,
		SourceURI:         cfg.SourceURI,
		SourceSHA256:      cfg.SourceSHA256,
		SourceRevision:    cfg.SourceRevision,
		CudaEnabled:       cfg.CudaEnabled,
		CudaArchitectures: cfg.CudaArchitectures,
		BuildToolchain:    "cmake+ninja+cuda",
		RuntimeBuild:      cfg.RuntimeBuild,
	}, true
}
