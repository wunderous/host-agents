package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type EnsureHostToolArgs struct {
	Tool string `json:"tool"`
}

// InstallIncusStackArgs describes the host virtualization prerequisite gate.
// The operation is intentionally explicit: it upgrades packages but never
// tears down instances or changes the Incus storage pool.
type InstallIncusStackArgs struct {
	IncusPackage string   `json:"incusPackage,omitempty"`
	QemuPackage  string   `json:"qemuPackage,omitempty"`
	GPUPackages  []string `json:"gpuPackages,omitempty"`
	IncusChannel string   `json:"incusChannel,omitempty"`
	IncusVersion string   `json:"incusVersion,omitempty"`
	InstallQEMU  bool     `json:"installQemu,omitempty"`
}

func (s *HostOperationsService) InstallIncusStack(args InstallIncusStackArgs, onData func(string)) (map[string]any, error) {
	if err := s.requireSharedHostOwner("install_incus_stack"); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("install_incus_stack is unsupported on %s host agents", runtime.GOOS)
	}
	incusPackage := strings.TrimSpace(args.IncusPackage)
	if incusPackage == "" {
		incusPackage = "incus"
	}
	qemuPackage := strings.TrimSpace(args.QemuPackage)
	if qemuPackage == "" {
		qemuPackage = "qemu-system-x86"
	}
	channel := strings.TrimSpace(args.IncusChannel)
	if channel == "" {
		channel = "stable"
	}
	if channel != "stable" && channel != "lts-7.0" && channel != "lts-6.0" {
		return nil, errors.New("incusChannel must be stable, lts-7.0, or lts-6.0")
	}
	packages := []string{incusPackage}
	if args.InstallQEMU {
		packages = append(packages, qemuPackage)
	}
	optional := map[string]string{}
	for _, candidate := range []string{"virglrenderer2", "libvirglrenderer1"} {
		if packageAvailable(candidate) {
			packages = append(packages, candidate)
			optional["virglrenderer"] = candidate
			break
		}
	}
	for _, pkg := range args.GPUPackages {
		if p := strings.TrimSpace(pkg); p != "" {
			if packageAvailable(p) {
				packages = append(packages, p)
				optional[p] = "available"
			} else {
				optional[p] = "unavailable_in_configured_repositories"
			}
		}
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return nil, errors.New("apt-get is required to install Incus/QEMU prerequisites")
	}
	if onData != nil {
		onData("Refreshing virtualization package indexes...")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := s.configureZabblyIncusRepository(ctx, channel); err != nil {
		return nil, fmt.Errorf("configure Incus package repository: %w", err)
	}
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "update"); err != nil {
		return nil, fmt.Errorf("update virtualization packages: %w", err)
	}
	if err := runPrivilegedPackageCommand(ctx, "dpkg", "--configure", "-a"); err != nil {
		return nil, fmt.Errorf("repair interrupted package state: %w", err)
	}
	incusVersion := strings.TrimSpace(args.IncusVersion)
	if incusVersion == "" {
		incusVersion = "7.2"
	}
	// The Zabbly stable channel advances minor/patch releases (e.g. 7.3) while
	// the requested pin stays on the same major line; accept candidates that
	// share the requested major version instead of requiring an exact match.
	if candidate := aptCandidate(incusPackage); candidate != "" && !incusCandidateSatisfies(candidate, incusVersion) {
		return nil, fmt.Errorf("Incus repository candidate %q does not satisfy requested version %q", candidate, incusVersion)
	}
	argsInstall := append([]string{"apt-get", "install", "-y"}, packages...)
	if candidate := aptCandidate(incusPackage); candidate != "" {
		argsInstall = append(argsInstall, incusPackage+"="+candidate)
	}
	if err := runPrivilegedPackageCommand(ctx, argsInstall[0], argsInstall[1:]...); err != nil {
		return nil, fmt.Errorf("install virtualization packages: %w", err)
	}
	// The incus-admin group is created by the Incus package. Granting access
	// before installation makes clean-host bootstrap fail with "group does not
	// exist". A root-run bootstrap agent does not need the supplemental group.
	if user := currentUserName(); user != "" && user != "root" {
		if err := runPrivilegedPackageCommand(ctx, "usermod", "-aG", "incus-admin", user); err != nil {
			return nil, fmt.Errorf("grant Incus admin access: %w", err)
		}
	}
	if err := runPrivilegedPackageCommand(ctx, "systemctl", "enable", "--now", "incus.service"); err != nil {
		return nil, fmt.Errorf("start Incus daemon: %w", err)
	}
	if err := s.ensureIncusContainerRuntime(onData); err != nil {
		return nil, fmt.Errorf("initialize Incus container runtime: %w", err)
	}
	result := virtualizationVersions()
	result["optionalPackages"] = optional
	return result, nil
}

func (s *HostOperationsService) ensureIncusContainerRuntime(onData func(string)) error {
	storage, err := s.commandRunner([]string{"storage", "list", "--format", "json"}, onData, 30*time.Second)
	if err != nil || storage.ExitCode != 0 {
		return fmt.Errorf("inspect storage pools: %s", firstNonEmpty(storage.Stderr, storage.Stdout, errString(err, "incus storage list failed")))
	}
	if !strings.Contains(storage.Stdout, `"name":"default"`) {
		created, createErr := s.commandRunner([]string{"storage", "create", "default", "dir"}, onData, 2*time.Minute)
		if createErr != nil || created.ExitCode != 0 {
			return fmt.Errorf("create default storage pool: %s", firstNonEmpty(created.Stderr, created.Stdout, errString(createErr, "incus storage create failed")))
		}
	}
	network, err := s.commandRunner([]string{"network", "list", "--format", "json"}, onData, 30*time.Second)
	if err != nil || network.ExitCode != 0 {
		return fmt.Errorf("inspect networks: %s", firstNonEmpty(network.Stderr, network.Stdout, errString(err, "incus network list failed")))
	}
	if !strings.Contains(network.Stdout, `"name":"incusbr0"`) {
		created, createErr := s.commandRunner([]string{"network", "create", "incusbr0", "ipv4.address=10.0.100.1/24", "ipv4.nat=true", "ipv6.address=none"}, onData, 2*time.Minute)
		if createErr != nil || created.ExitCode != 0 {
			return fmt.Errorf("create Incus container network: %s", firstNonEmpty(created.Stderr, created.Stdout, errString(createErr, "incus network create failed")))
		}
	}
	profile, err := s.commandRunner([]string{"profile", "device", "show", "default"}, onData, 30*time.Second)
	if err != nil || profile.ExitCode != 0 {
		return fmt.Errorf("inspect default profile: %s", firstNonEmpty(profile.Stderr, profile.Stdout, errString(err, "incus profile device show failed")))
	}
	if !incusProfileHasDevice(profile.Stdout, "root") {
		added, addErr := s.commandRunner([]string{"profile", "device", "add", "default", "root", "disk", "path=/", "pool=default"}, onData, 2*time.Minute)
		if addErr != nil || added.ExitCode != 0 {
			return fmt.Errorf("attach default root disk: %s", firstNonEmpty(added.Stderr, added.Stdout, errString(addErr, "incus profile device add failed")))
		}
	}
	// A clean Incus install can have a default profile with only its root
	// device. Reconcile the network device as part of the runtime contract so
	// every system container receives an interface backed by the managed bridge.
	if !incusProfileHasDevice(profile.Stdout, "eth0") {
		added, addErr := s.commandRunner([]string{
			"profile", "device", "add", "default", "eth0", "nic",
			"nictype=bridged", "parent=incusbr0", "name=eth0",
		}, onData, 2*time.Minute)
		if addErr != nil || added.ExitCode != 0 {
			return fmt.Errorf("attach default container network: %s", firstNonEmpty(added.Stderr, added.Stdout, errString(addErr, "incus profile network device add failed")))
		}
	} else {
		for _, setting := range [][2]string{{"nictype", "bridged"}, {"parent", "incusbr0"}, {"name", "eth0"}} {
			updated, updateErr := s.commandRunner([]string{
				"profile", "device", "set", "default", "eth0", setting[0], setting[1],
			}, onData, 2*time.Minute)
			if updateErr != nil || updated.ExitCode != 0 {
				return fmt.Errorf("reconcile default container network %s: %s", setting[0], firstNonEmpty(updated.Stderr, updated.Stdout, errString(updateErr, "incus profile network device update failed")))
			}
		}
	}
	return nil
}

func incusProfileHasDevice(profile, deviceName string) bool {
	want := strings.TrimSpace(deviceName) + ":"
	for _, line := range strings.Split(profile, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func aptCandidate(packageName string) string {
	res, err := exec.Command("apt-cache", "policy", packageName).CombinedOutput()
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`(?m)^\s*Candidate:\s*(\S+)`).FindStringSubmatch(string(res))
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// incusCandidateSatisfies reports whether an apt candidate version (which may
// carry an epoch prefix such as "1:7.3-ubuntu26.04-...") matches the requested
// Incus version pin on the same major line.
func incusCandidateSatisfies(candidate, requested string) bool {
	requestedMajor := strings.SplitN(strings.TrimSpace(requested), ".", 2)[0]
	withoutEpoch := strings.TrimSpace(candidate)
	if colon := strings.Index(withoutEpoch, ":"); colon >= 0 {
		withoutEpoch = withoutEpoch[colon+1:]
	}
	return strings.HasPrefix(withoutEpoch, requestedMajor)
}

func (s *HostOperationsService) configureZabblyIncusRepository(ctx context.Context, channel string) error {
	keyFile, err := os.CreateTemp("", "opute-zabbly-key-*")
	if err != nil {
		return err
	}
	keyPath := keyFile.Name()
	_ = keyFile.Close()
	defer os.Remove(keyPath)
	curl := exec.CommandContext(ctx, "curl", "-fsSL", "https://pkgs.zabbly.com/key.asc", "-o", keyPath)
	if output, err := curl.CombinedOutput(); err != nil {
		return fmt.Errorf("download repository key: %s", strings.TrimSpace(string(output)))
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil || !strings.Contains(string(keyBytes), "BEGIN PGP PUBLIC KEY BLOCK") {
		return errors.New("repository key did not look like an armored OpenPGP key")
	}
	osRelease, _ := os.ReadFile("/etc/os-release")
	codenameMatch := regexp.MustCompile(`(?m)^VERSION_CODENAME=(\S+)`).FindStringSubmatch(string(osRelease))
	if len(codenameMatch) != 2 {
		return errors.New("could not resolve Ubuntu VERSION_CODENAME")
	}
	archRes, err := exec.Command("dpkg", "--print-architecture").Output()
	if err != nil {
		return fmt.Errorf("resolve dpkg architecture: %w", err)
	}
	source, err := os.CreateTemp("", "opute-zabbly-source-*")
	if err != nil {
		return err
	}
	sourcePath := source.Name()
	defer os.Remove(sourcePath)
	_, err = source.WriteString(fmt.Sprintf("Enabled: yes\nTypes: deb\nURIs: https://pkgs.zabbly.com/incus/%s\nSuites: %s\nComponents: main\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/zabbly.asc\n", channel, strings.TrimSpace(codenameMatch[1]), strings.TrimSpace(string(archRes))))
	if closeErr := source.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := runPrivilegedPackageCommand(ctx, "install", "-d", "/etc/apt/keyrings"); err != nil {
		return err
	}
	if err := runPrivilegedPackageCommand(ctx, "install", "-m", "0644", keyPath, "/etc/apt/keyrings/zabbly.asc"); err != nil {
		return err
	}
	return runPrivilegedPackageCommand(ctx, "install", "-m", "0644", sourcePath, "/etc/apt/sources.list.d/zabbly-incus.sources")
}

func packageAvailable(name string) bool {
	res, err := exec.Command("apt-cache", "show", name).CombinedOutput()
	return err == nil && strings.Contains(string(res), "Package:")
}

func virtualizationVersions() map[string]any {
	out := map[string]any{}
	for name, command := range map[string][]string{
		"incus":         {"incus", "version"},
		"qemu":          {"qemu-system-x86_64", "--version"},
		"virglrenderer": {"dpkg-query", "-W", "-f=${Version}", "virglrenderer2"},
	} {
		if res, err := exec.Command(command[0], command[1:]...).CombinedOutput(); err == nil {
			out[name] = strings.TrimSpace(string(res))
		}
	}
	return out
}

func (s *HostOperationsService) ProbeIncusGPU(args map[string]any) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("probe_incus_gpu is unsupported on %s host agents", runtime.GOOS)
	}
	result := virtualizationVersions()
	result["dxg"] = fileExists("/dev/dxg")
	result["wslGpuLibraries"] = fileExists("/usr/lib/wsl/lib/libcuda.so") || fileExists("/usr/lib/wsl/lib/libcuda.so.1")
	result["nvidiaSmi"] = commandAvailable("nvidia-smi") || fileExists("/usr/lib/wsl/lib/nvidia-smi")
	result["incusGpuDevice"] = commandAvailable("incus")
	incusOK := versionAtLeast(fmt.Sprint(result["incus"]), 7, 2)
	qemuRequired, _ := args["qemuRequired"].(bool)
	qemuVersionOK := versionAtLeast(fmt.Sprint(result["qemu"]), 11, 0)
	qemuOK := !qemuRequired || qemuVersionOK
	result["versionGate"] = map[string]any{"incusAtLeast7_2": incusOK, "qemuRequired": qemuRequired, "qemuAtLeast11": qemuVersionOK}
	result["status"] = "blocked"
	if !incusOK || !qemuOK {
		result["status"] = "blocked_version_gate"
	} else if result["dxg"] == true && result["wslGpuLibraries"] == true && result["nvidiaSmi"] == true {
		result["status"] = "ready_for_host_probe"
	}
	return result, nil
}

func versionAtLeast(value string, wantMajor, wantMinor int) bool {
	re := regexp.MustCompile(`([0-9]+)\.([0-9]+)`) // package output may contain a label before the version
	m := re.FindStringSubmatch(value)
	if len(m) != 3 {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > wantMajor || (major == wantMajor && minor >= wantMinor)
}

func fileExists(path string) bool       { _, err := os.Stat(path); return err == nil }
func commandAvailable(name string) bool { _, err := exec.LookPath(name); return err == nil }

func currentUserName() string {
	if user, err := osuser.Current(); err == nil && user.Username != "" {
		return user.Username
	}
	return "opute"
}

// EnsureHostTool installs a small, explicitly allowlisted set of generic host
// build/runtime tools. Application-specific setup remains outside the agent.
func (s *HostOperationsService) EnsureHostTool(args EnsureHostToolArgs, onData func(string)) (map[string]any, error) {
	if err := s.requireSharedHostOwner("ensure_host_tool"); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("ensure_host_tool is unsupported on %s host agents", runtime.GOOS)
	}
	tool := strings.ToLower(strings.TrimSpace(args.Tool))
	if tool == "bun" {
		return s.ensureBunTool(onData)
	}
	if tool == "helm" {
		return s.ensureHelmTool(onData)
	}
	packageName, ok := hostToolPackages[tool]
	if !ok {
		return nil, errors.New("tool must be one of bun, gcc, g++, go, podman, buildah, buildkitd, cloudflared, helm, cmake, ninja, or nvcc")
	}
	if path, err := exec.LookPath(tool); err == nil {
		return map[string]any{"tool": tool, "path": path, "available": true, "alreadyAvailable": true}, nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return nil, fmt.Errorf("%s is not installed and apt-get is unavailable", tool)
	}
	if onData != nil {
		onData(fmt.Sprintf("Installing host tool package %s...", packageName))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "update"); err != nil {
		return nil, fmt.Errorf("update apt package indexes: %w", err)
	}
	if err := runPrivilegedPackageCommand(ctx, "apt-get", "install", "-y", packageName); err != nil {
		return nil, fmt.Errorf("install host tool %s: %w", tool, err)
	}
	path, err := exec.LookPath(tool)
	if err != nil {
		return nil, fmt.Errorf("host tool %s was installed but remains unavailable: %w", tool, err)
	}
	return map[string]any{"tool": tool, "path": path, "available": true, "alreadyAvailable": false}, nil
}

func (s *HostOperationsService) ensureBunTool(onData func(string)) (map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil, errors.New("bun is not installed and user home is unavailable for a user-local install")
	}
	installDir := filepath.Join(home, ".bun", "bin")
	bunPath := filepath.Join(installDir, "bun")
	if _, statErr := os.Stat(bunPath); statErr == nil {
		return map[string]any{"tool": "bun", "path": bunPath, "available": true, "alreadyAvailable": true}, nil
	}
	if onData != nil {
		onData(fmt.Sprintf("Installing Bun into %s...", installDir))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	installScript := fmt.Sprintf(
		`curl -fsSL https://bun.sh/install | bash -s -- --no-modify-shell && test -x %s`,
		shellEscape(bunPath),
	)
	res, runErr := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", installScript}, onData, 0)
	if runErr != nil {
		return nil, runErr
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "bun install failed"))
	}
	if _, statErr := os.Stat(bunPath); statErr != nil {
		return nil, fmt.Errorf("bun was installed but remains unavailable: %w", statErr)
	}
	return map[string]any{"tool": "bun", "path": bunPath, "available": true, "alreadyAvailable": false}, nil
}

func (s *HostOperationsService) ensureHelmTool(onData func(string)) (map[string]any, error) {
	if path, err := exec.LookPath("helm"); err == nil {
		return map[string]any{"tool": "helm", "path": path, "available": true, "alreadyAvailable": true}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil, errors.New("helm is not installed and user home is unavailable for a user-local install")
	}
	installDir := filepath.Join(home, ".local", "bin")
	if onData != nil {
		onData(fmt.Sprintf("Installing helm into %s...", installDir))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	installScript := fmt.Sprintf(
		`mkdir -p %s && curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | HELM_INSTALL_DIR=%s USE_SUDO=false bash`,
		shellEscape(installDir),
		shellEscape(installDir),
	)
	res, runErr := s.hostCommandRunnerContext(ctx, []string{"bash", "-lc", installScript}, onData, 0)
	if runErr != nil {
		return nil, runErr
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstNonEmpty(res.Stderr, res.Stdout, "helm install failed"))
	}
	path := filepath.Join(installDir, "helm")
	if _, statErr := os.Stat(path); statErr != nil {
		if looked, lookErr := exec.LookPath("helm"); lookErr == nil {
			path = looked
		} else {
			return nil, fmt.Errorf("helm was installed but remains unavailable: %w", statErr)
		}
	}
	return map[string]any{"tool": "helm", "path": path, "available": true, "alreadyAvailable": false}, nil
}

var hostToolPackages = map[string]string{
	"gcc":         "gcc",
	"g++":         "g++",
	"go":          "golang-go",
	"podman":      "podman",
	"buildah":     "buildah",
	"buildkitd":   "moby-buildkit",
	"cloudflared": "cloudflared",
	"cmake":       "cmake",
	"ninja":       "ninja-build",
	"nvcc":        "nvidia-cuda-toolkit",
}
