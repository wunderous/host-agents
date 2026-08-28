package kubernetes

import (
	"encoding/json"
	"fmt"
	"strings"
)

type InstallHelmChartArgs struct {
	VMName      string
	ReleaseName string
	ChartSource string
	Namespace   string
	Repo        string
	Values      string
}

type UninstallHelmChartArgs struct {
	VMName      string
	ReleaseName string
	Namespace   string
}

func indentYAMLBlock(values string) string {
	lines := strings.Split(strings.TrimRight(values, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}

func renderHelmChartManifest(args InstallHelmChartArgs) string {
	namespace := strings.TrimSpace(args.Namespace)
	if namespace == "" {
		namespace = "kube-system"
	}
	lines := []string{
		"apiVersion: helm.cattle.io/v1",
		"kind: HelmChart",
		"metadata:",
		fmt.Sprintf("  name: %s", args.ReleaseName),
		"  namespace: kube-system",
		"spec:",
		fmt.Sprintf("  chart: %s", args.ChartSource),
	}
	if repo := strings.TrimSpace(args.Repo); repo != "" {
		lines = append(lines, fmt.Sprintf("  repo: %s", repo))
	}
	lines = append(lines, fmt.Sprintf("  targetNamespace: %s", namespace))
	if values := strings.TrimSpace(args.Values); values != "" {
		lines = append(lines, "  valuesContent: |")
		lines = append(lines, indentYAMLBlock(values))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) InstallHelmChart(args InstallHelmChartArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, fmt.Errorf("vmName is required")
	}
	releaseName := strings.TrimSpace(args.ReleaseName)
	if releaseName == "" {
		return nil, fmt.Errorf("releaseName is required")
	}
	chartSource := strings.TrimSpace(args.ChartSource)
	if chartSource == "" {
		return nil, fmt.Errorf("chartSource is required")
	}
	namespace := strings.TrimSpace(args.Namespace)
	if namespace == "" {
		namespace = "kube-system"
	}

	if namespace != "kube-system" {
		if err := s.EnsureHelmNamespace(vmName, namespace); err != nil {
			return nil, err
		}
	}

	manifest := renderHelmChartManifest(args)
	targetURI, err := s.TargetURI(vmName)
	if err != nil {
		return nil, err
	}
	if _, err := s.ApplyManifest(ApplyManifestArgs{URI: targetURI, Manifest: manifest}, onData); err != nil {
		return nil, err
	}

	return map[string]any{
		"vmName":      vmName,
		"releaseName": releaseName,
		"chartSource": chartSource,
		"namespace":   namespace,
		"status":      "installing",
	}, nil
}

func (s *Service) UninstallHelmChart(args UninstallHelmChartArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, fmt.Errorf("vmName is required")
	}
	releaseName := strings.TrimSpace(args.ReleaseName)
	if releaseName == "" {
		return nil, fmt.Errorf("releaseName is required")
	}
	namespace := strings.TrimSpace(args.Namespace)
	if namespace == "" {
		namespace = "kube-system"
	}

	targetURI, err := s.TargetURI(vmName)
	if err != nil {
		return nil, err
	}
	if _, err := s.DeleteK8sResource(K8sResourceArgs{URI: targetURI, Kind: "helmchart", ResourceName: releaseName, Namespace: namespace}, onData); err != nil {
		return nil, err
	}

	return map[string]any{
		"vmName":      vmName,
		"releaseName": releaseName,
		"status":      "uninstalled",
	}, nil
}

func HelmValuesYAML(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return ""
	}
}
