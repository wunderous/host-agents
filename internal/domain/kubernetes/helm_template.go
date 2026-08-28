package kubernetes

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wunderous/host-agents/internal/textutil"
)

// RenderHelmTemplateArgs runs `helm template` on the host agent machine.
type RenderHelmTemplateArgs struct {
	ChartPath   string   `json:"chartPath"`
	ReleaseName string   `json:"releaseName"`
	ValuesFiles []string `json:"valuesFiles,omitempty"`
	Set         []string `json:"set,omitempty"`
	Namespace   string   `json:"namespace,omitempty"`
}

func (s *Service) RenderHelmTemplate(args RenderHelmTemplateArgs, onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("render_helm_template is unsupported on %s host agents", runtime.GOOS)
	}
	chartPath := strings.TrimSpace(args.ChartPath)
	releaseName := strings.TrimSpace(args.ReleaseName)
	if chartPath == "" {
		return nil, errors.New("chartPath is required")
	}
	if releaseName == "" {
		return nil, errors.New("releaseName is required")
	}
	absChart, err := filepath.Abs(chartPath)
	if err != nil {
		return nil, fmt.Errorf("resolve chartPath: %w", err)
	}
	if info, statErr := os.Stat(absChart); statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("chartPath must be an existing directory: %s", absChart)
	}
	toolInfo, err := s.deps.EnsureHostTool("helm", onData)
	if err != nil {
		return nil, err
	}
	helmPath, _ := toolInfo["path"].(string)
	if strings.TrimSpace(helmPath) == "" {
		var lookErr error
		helmPath, lookErr = lookupHostTool("helm")
		if lookErr != nil {
			return nil, lookErr
		}
	}
	argv := []string{helmPath, "template", releaseName, absChart}
	if namespace := strings.TrimSpace(args.Namespace); namespace != "" {
		argv = append(argv, "--namespace", namespace)
	}
	for _, valuesFile := range args.ValuesFiles {
		valuesFile = strings.TrimSpace(valuesFile)
		if valuesFile == "" {
			continue
		}
		absValues, absErr := filepath.Abs(valuesFile)
		if absErr != nil {
			return nil, fmt.Errorf("resolve values file: %w", absErr)
		}
		if _, statErr := os.Stat(absValues); statErr != nil {
			return nil, fmt.Errorf("values file not found: %s", absValues)
		}
		argv = append(argv, "-f", absValues)
	}
	for _, setArg := range args.Set {
		setArg = strings.TrimSpace(setArg)
		if setArg == "" {
			continue
		}
		argv = append(argv, "--set", setArg)
	}
	if onData != nil {
		onData(fmt.Sprintf("Rendering Helm chart %q...", releaseName))
	}
	res, runErr := s.shared.HostCommandRunner(argv, onData, 10*time.Minute)
	if runErr != nil {
		return nil, runErr
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", textutil.FirstNonEmpty(res.Stderr, res.Stdout, "helm template failed"))
	}
	return map[string]any{
		"releaseName": releaseName,
		"chartPath":   absChart,
		"manifest":    res.Stdout,
	}, nil
}

func lookupHostTool(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("host tool %s is not available: %w", name, err)
	}
	return path, nil
}
