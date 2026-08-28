package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/incus"
	"github.com/wunderous/host-agents/internal/hostagent"
)

func init() {
	register(toolname.ProvisionContainer, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		nesting := optionalBoolField(args, "nesting")
		out, err := svc.Incus().ProvisionContainer(incus.ProvisionContainerArgs{
			ContainerName: stringField(args, "containerName"),
			Image:         stringField(args, "image"),
			CPUs:          intField(args, "cpus"),
			Memory:        stringField(args, "memory"),
			Disk:          stringField(args, "disk"),
			GPU:           boolField(args, "gpu"),
			WSLGpuLibs:    boolField(args, "wslGpuLibs"),
			Nesting:       nesting,
			Port:          intField(args, "port"),
			ModelVolume:   stringField(args, "modelVolume"),
		}, onData)
		if err != nil {
			return nil, err
		}
		payload := map[string]any{
			"uri":           out.URI,
			"containerName": out.ContainerName,
			"vmName":        out.ContainerName,
			"image":         out.Image,
			"status":        out.Status,
			"instanceType":  out.InstanceType,
		}
		return structuredResult(payload, fmt.Sprintf("Provisioned Incus container '%s'.", out.ContainerName)), nil
	})
}

func init() {
	register(toolname.ProbeGPUContainer, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().ProbeGPUContainer(onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "System container GPU capability report generated"), nil
	})
}

func init() {
	register(toolname.ResetIncusStack, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Incus().ResetIncusStack(ctx, incus.ResetIncusStackArgs{
			InstanceNames:               stringSliceField(args, "instanceNames"),
			InstancePrefix:              stringField(args, "instancePrefix"),
			Confirm:                     boolField(args, "confirm"),
			Reinstall:                   boolField(args, "reinstall"),
			DryRun:                      boolField(args, "dryRun"),
			DisposableHostFingerprint:   stringField(args, "disposableHostFingerprint"),
			ExpectedHostFingerprint:     stringField(args, "expectedHostFingerprint"),
			DisposableHostAuthorization: stringField(args, "disposableHostAuthorization"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Incus stack reset phase completed"), nil
	})
}

func init() {
	register(toolname.GetVMInfo, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		vmName := vmNameFromBinding(binding)
		if vmName == "" {
			return nil, fmt.Errorf("vmName is required")
		}
		fast, _ := args["fast"].(bool)
		out, err := svc.Incus().GetVMInfo(vmName, fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ListVMs, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		fast, _ := args["fast"].(bool)
		out, err := svc.Incus().ListVMs(fast)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}
