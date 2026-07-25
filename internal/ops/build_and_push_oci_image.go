package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var safeOciImageRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

// BuildAndPushOciImageArgs builds a generic OCI image from a host-local context
// directory and pushes it to a caller-selected registry. The host agent does not
// know Opute application layout; the MCP client stages the build context.
type BuildAndPushOciImageArgs struct {
	ContextDir       string `json:"contextDir"`
	Dockerfile       string `json:"dockerfile,omitempty"`
	Image            string `json:"image"`
	Builder          string `json:"builder,omitempty"`
	InsecureRegistry bool   `json:"insecureRegistry,omitempty"`
	Platform         string `json:"platform,omitempty"`
}

// BuildAndPushOciImage ensures a builder is available, builds the image, and
// pushes it. Long-running progress is streamed through onData.
func (s *HostOperationsService) BuildAndPushOciImage(args BuildAndPushOciImageArgs, onData func(string)) (map[string]any, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("build_and_push_oci_image is unsupported on %s host agents", runtime.GOOS)
	}
	contextDir := strings.TrimSpace(args.ContextDir)
	image := strings.TrimSpace(args.Image)
	if contextDir == "" {
		return nil, errors.New("contextDir is required")
	}
	if image == "" {
		return nil, errors.New("image is required")
	}
	if !safeOciImageRef.MatchString(image) || strings.ContainsAny(image, " \t\r\n") {
		return nil, errors.New("image reference contains invalid characters")
	}
	absContext, err := filepath.Abs(contextDir)
	if err != nil {
		return nil, fmt.Errorf("resolve contextDir: %w", err)
	}
	info, err := os.Stat(absContext)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("contextDir must be an existing directory: %s", absContext)
	}
	dockerfile := strings.TrimSpace(args.Dockerfile)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	dockerfilePath := dockerfile
	if !filepath.IsAbs(dockerfilePath) {
		dockerfilePath = filepath.Join(absContext, dockerfile)
	}
	if _, err := os.Stat(dockerfilePath); err != nil {
		return nil, fmt.Errorf("dockerfile not found: %s", dockerfilePath)
	}

	builderInfo, err := s.EnsureOciBuilder(EnsureOciBuilderArgs{Builder: args.Builder}, onData)
	if err != nil {
		return nil, err
	}
	builder, _ := builderInfo["builder"].(string)
	if builder == "" || builder == "buildkit" {
		builder = "podman"
		if path, lookErr := exec.LookPath("podman"); lookErr != nil {
			_ = path
			if _, lookErr = exec.LookPath("buildah"); lookErr == nil {
				builder = "buildah"
			} else {
				return nil, errors.New("podman or buildah is required to build and push images")
			}
		}
	}

	platform := strings.TrimSpace(args.Platform)
	if onData != nil {
		onData(fmt.Sprintf("Building %s with %s", image, builder))
	}
	buildCtx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	var buildCmd *exec.Cmd
	switch builder {
	case "podman":
		argv := []string{"build", "-f", dockerfilePath, "-t", image}
		if platform != "" {
			argv = append(argv, "--platform", platform)
		}
		argv = append(argv, absContext)
		buildCmd = exec.CommandContext(buildCtx, "podman", argv...)
	case "buildah":
		argv := []string{"bud", "-f", dockerfilePath, "-t", image}
		if platform != "" {
			argv = append(argv, "--platform", platform)
		}
		argv = append(argv, absContext)
		buildCmd = exec.CommandContext(buildCtx, "buildah", argv...)
	default:
		return nil, fmt.Errorf("unsupported builder %q for build_and_push_oci_image", builder)
	}
	if err := runStreamingCommand(buildCmd, onData); err != nil {
		return nil, fmt.Errorf("build image: %w", err)
	}

	if onData != nil {
		onData(fmt.Sprintf("Pushing %s", image))
	}
	pushCtx, pushCancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer pushCancel()
	var pushCmd *exec.Cmd
	switch builder {
	case "podman":
		argv := []string{"push"}
		if args.InsecureRegistry {
			argv = append(argv, "--tls-verify=false")
		}
		argv = append(argv, image)
		pushCmd = exec.CommandContext(pushCtx, "podman", argv...)
	case "buildah":
		argv := []string{"push"}
		if args.InsecureRegistry {
			argv = append(argv, "--tls-verify=false")
		}
		argv = append(argv, image)
		pushCmd = exec.CommandContext(pushCtx, "buildah", argv...)
	}
	if err := runStreamingCommand(pushCmd, onData); err != nil {
		return nil, fmt.Errorf("push image: %w", err)
	}

	return map[string]any{
		"image":            image,
		"builder":          builder,
		"contextDir":       absContext,
		"dockerfile":       dockerfilePath,
		"insecureRegistry": args.InsecureRegistry,
		"pushed":           true,
	}, nil
}

func runStreamingCommand(cmd *exec.Cmd, onData func(string)) error {
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if onData != nil && text != "" {
		// Cap progress chatter; keep the tail for diagnosis.
		const max = 4000
		if len(text) > max {
			onData(text[len(text)-max:])
		} else {
			onData(text)
		}
	}
	if err != nil {
		if text == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, text)
	}
	return nil
}
