package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PrepareHostAgentArtifactsArgs builds Linux host-agent binaries for Opute platform images.
type PrepareHostAgentArtifactsArgs struct {
	SourceDir string   `json:"sourceDir"`
	DestDir   string   `json:"destDir"`
	Archs     []string `json:"archs,omitempty"`
}

func (s *HostOperationsService) PrepareHostAgentArtifacts(args PrepareHostAgentArtifactsArgs, onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("prepare_host_agent_artifacts is unsupported on %s host agents", runtime.GOOS)
	}
	sourceDir := strings.TrimSpace(args.SourceDir)
	destDir := strings.TrimSpace(args.DestDir)
	if sourceDir == "" {
		return nil, errors.New("sourceDir is required")
	}
	if destDir == "" {
		return nil, errors.New("destDir is required")
	}
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve sourceDir: %w", err)
	}
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return nil, fmt.Errorf("resolve destDir: %w", err)
	}
	if info, statErr := os.Stat(absSource); statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("sourceDir must be an existing directory: %s", absSource)
	}
	if !hostAgentArtifactDestAllowed(absDest, absSource) {
		return nil, fmt.Errorf("destDir must be under sourceDir or ~/.opute/host-agent-build")
	}
	archs := args.Archs
	if len(archs) == 0 {
		archs = []string{"amd64", "arm64"}
	}
	if _, err := s.EnsureHostTool(EnsureHostToolArgs{Tool: "go"}, onData); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return nil, fmt.Errorf("create destDir: %w", err)
	}
	built := make([]string, 0, len(archs))
	for _, arch := range archs {
		arch = strings.TrimSpace(arch)
		if arch != "amd64" && arch != "arm64" {
			return nil, fmt.Errorf("unsupported arch %q (use amd64 or arm64)", arch)
		}
		outputName := "host-agent-linux-x64"
		if arch == "arm64" {
			outputName = "host-agent-linux-arm64"
		}
		outputPath := filepath.Join(absDest, outputName)
		if onData != nil {
			onData(fmt.Sprintf("Building %s for linux/%s...", outputName, arch))
		}
		buildScript := fmt.Sprintf(
			`cd %s && GOOS=linux GOARCH=%s go build -buildvcs=false -ldflags=-s -w -o %s github.com/wunderous/host-agents/cmd/opute-host-agent`,
			shellEscape(absSource),
			arch,
			shellEscape(outputPath),
		)
		res, runErr := s.hostCommandRunner([]string{"bash", "-lc", buildScript}, onData, 15*time.Minute)
		if runErr != nil {
			return nil, runErr
		}
		if res.ExitCode != 0 {
			return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, fmt.Sprintf("go build failed for %s", arch)))
		}
		built = append(built, outputName)
	}
	return map[string]any{
		"sourceDir": absSource,
		"destDir":   absDest,
		"artifacts": built,
	}, nil
}

func hostAgentArtifactDestAllowed(dest, source string) bool {
	cleanDest := filepath.Clean(dest)
	cleanSource := filepath.Clean(source)
	if cleanDest == cleanSource || strings.HasPrefix(cleanDest, cleanSource+string(os.PathSeparator)) {
		return true
	}
	if strings.HasSuffix(cleanDest, string(os.PathSeparator)+".opute-host-agent-build") {
		if parent := filepath.Dir(cleanDest); parent != "" {
			if info, err := os.Stat(parent); err == nil && info.IsDir() {
				return true
			}
		}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return false
	}
	root := filepath.Join(home, ".opute", "host-agent-build")
	return cleanDest == root || strings.HasPrefix(cleanDest, root+string(os.PathSeparator))
}
