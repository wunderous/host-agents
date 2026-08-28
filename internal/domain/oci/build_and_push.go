package oci

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
// directory and pushes it to a caller-selected registry. The host agent does
// not know Opute application layout; the MCP client stages the build context.
type BuildAndPushOciImageArgs struct {
	ContextDir       string            `json:"contextDir"`
	Dockerfile       string            `json:"dockerfile,omitempty"`
	Image            string            `json:"image"`
	Builder          string            `json:"builder,omitempty"`
	InsecureRegistry bool              `json:"insecureRegistry,omitempty"`
	Platform         string            `json:"platform,omitempty"`
	BuildArgs        map[string]string `json:"buildArgs,omitempty"`
	// UntagAfterPush defaults to true when omitted: after a successful push
	// the local tag of the built image is removed so a later --pull=never
	// build cannot silently reuse a stale local tag, while the image layers
	// remain available to the age-gated storage policy. Set it to false to
	// keep the pushed tag in the local store.
	UntagAfterPush *bool `json:"untagAfterPush,omitempty"`
}

// BuildAndPushOciImage ensures a builder is available, builds the image, and
// pushes it. Long-running progress is streamed through onData.
func (s *Service) BuildAndPushOciImage(ctx context.Context, args BuildAndPushOciImageArgs, onData func(string)) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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

	// Untag-after-push defaults to true. Removing the pushed tag from the
	// local store is a deliberate storage-hygiene step: the image content is
	// already durable in the caller-selected registry, and future local
	// builds with the same tag must pull fresh from that registry instead of
	// silently reusing a stale local digest. The image layers themselves are
	// intentionally retained so the storage policy (not this operation)
	// decides when they become reclaimable.
	imageRepo, imageDigestOnly := splitOciImageRef(image)
	untagAfterPush := true
	if args.UntagAfterPush != nil {
		untagAfterPush = *args.UntagAfterPush
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
	if err := validateOciBuildArgs(args.BuildArgs); err != nil {
		return nil, err
	}

	builderInfo, err := s.EnsureOciBuilder(EnsureOciBuilderArgs{Builder: args.Builder}, onData)
	if err != nil {
		return nil, err
	}
	builder, _ := builderInfo["builder"].(string)
	var runtimeAdapter containerRuntimeAdapter
	if builder == "podman" {
		runtimeAdapter, err = s.resolveContainerRuntime(ctx, "podman")
		if err != nil {
			return nil, err
		}
	} else if builder == "buildkit" {
		// BuildKit is retained as a legacy selection value, but this operation
		// still uses the supported image-store path when possible.
		if runtimeAdapter, err = s.resolveContainerRuntime(ctx, "auto"); err == nil {
			builder = runtimeAdapter.Name()
		} else if path, lookErr := s.shared.ContainerLookPath("buildah"); lookErr == nil {
			_ = path
			builder = "buildah"
		} else {
			return nil, errors.New("Podman is required for runtime-backed OCI storage")
		}
	}
	if args.InsecureRegistry && builder == "docker" {
		return nil, errors.New("insecureRegistry requires Docker daemon registry configuration; the host agent does not mutate daemon configuration")
	}
	ociStorageBeforeBuild, err := s.enforceOciStoragePolicy(ctx, builder, onData)
	if err != nil {
		return nil, fmt.Errorf("enforce OCI storage policy before build: %w", err)
	}

	platform := strings.TrimSpace(args.Platform)
	if onData != nil {
		onData(fmt.Sprintf("Building %s with %s", image, builder))
	}
	buildCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	if runtimeAdapter != nil {
		if err := runtimeAdapter.Build(buildCtx, dockerfilePath, image, platform, absContext, args.BuildArgs, onData); err != nil {
			return nil, fmt.Errorf("build image: %w", err)
		}
	} else if builder == "buildah" {
		argv := []string{"bud", "-f", dockerfilePath, "-t", image}
		if platform != "" {
			argv = append(argv, "--platform", platform)
		}
		argv = appendOciBuildArgs(argv, args.BuildArgs)
		argv = append(argv, absContext)
		if err := s.runContainerStreamingCommand(buildCtx, "buildah", argv, onData); err != nil {
			return nil, fmt.Errorf("build image: %w", err)
		}
	} else {
		return nil, fmt.Errorf("unsupported builder %q for build_and_push_oci_image", builder)
	}

	if onData != nil {
		onData(fmt.Sprintf("Pushing %s", image))
	}
	pushCtx, pushCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer pushCancel()
	if runtimeAdapter != nil {
		if err := runtimeAdapter.Push(pushCtx, image, args.InsecureRegistry, onData); err != nil {
			return nil, fmt.Errorf("push image: %w", err)
		}
	} else if builder == "buildah" {
		pushArgs := []string{"push"}
		if args.InsecureRegistry {
			pushArgs = append(pushArgs, "--tls-verify=false")
		}
		pushArgs = append(pushArgs, image)
		if err := s.runContainerStreamingCommand(pushCtx, "buildah", pushArgs, onData); err != nil {
			return nil, fmt.Errorf("push image: %w", err)
		}
	}

	result := map[string]any{
		"image":            image,
		"builder":          builder,
		"runtime":          builder,
		"contextDir":       absContext,
		"dockerfile":       dockerfilePath,
		"insecureRegistry": args.InsecureRegistry,
		"pushed":           true,
		"untagAfterPush":   untagAfterPush,
	}
	if untagAfterPush {
		switch {
		case runtimeAdapter == nil:
			// The legacy Buildah path is build-only and has no runtime
			// adapter; the pushed tag stays in place for that path.
			result["untagSkippedReason"] = "legacy buildah path has no runtime adapter"
		case imageDigestOnly:
			// A digest-pinned reference has no removable local tag of its
			// own; untagging it would target the hidden tag entry, which is
			// not the hygiene intent for a pushed digest reference.
			result["untagSkippedReason"] = "digest-pinned reference has no tag to remove"
		default:
			if untagErr := runtimeAdapter.Untag(ctx, image, onData); untagErr != nil {
				// The image has already been pushed successfully. Preserve
				// that result and surface the untag failure for diagnosis.
				result["untagWarning"] = untagErr.Error()
			} else {
				result["untaggedImage"] = image
				result["untaggedRepository"] = imageRepo
				if onData != nil {
					onData(fmt.Sprintf("Untagged local image %s (pushed content remains in %s)", image, imageRepo))
				}
			}
		}
	}
	if len(args.BuildArgs) > 0 {
		result["buildArgCount"] = len(args.BuildArgs)
	}
	if ociStorageBeforeBuild != nil {
		result["ociStorageBeforeBuild"] = ociStorageBeforeBuild
	}
	if ociStorageAfterBuild, storageErr := s.enforceOciStoragePolicy(ctx, builder, onData); storageErr != nil {
		// The image has already been pushed successfully. Preserve that result
		// and surface cleanup failure for the caller to diagnose.
		result["ociStoragePolicyWarning"] = storageErr.Error()
	} else if ociStorageAfterBuild != nil {
		result["ociStorageAfterBuild"] = ociStorageAfterBuild
	}
	return result, nil
}

var safeOciBuildArgName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateOciBuildArgs(buildArgs map[string]string) error {
	for name, value := range buildArgs {
		if !safeOciBuildArgName.MatchString(name) {
			return fmt.Errorf("buildArgs contains invalid name %q", name)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("buildArgs[%s] contains an invalid control character", name)
		}
	}
	return nil
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
