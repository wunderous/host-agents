package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/oci"
	"github.com/wunderous/host-agents/internal/hostagent"
)

func init() {
	register(toolname.InstallOCIRegistry, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Oci().InstallOCIRegistry(oci.InstallOCIRegistryArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Image: stringField(args, "image"), StorageSize: stringField(args, "storageSize"), StorageClass: stringField(args, "storageClass"), NodePort: intField(args, "nodePort")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "OCI registry deployment initiated."), nil
	})
}

func init() {
	register(toolname.GetOCIRegistryStatus, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Oci().GetOCIRegistryStatus(oci.InstallOCIRegistryArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Name: stringField(args, "name")})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.DeleteOCIRegistry, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Oci().DeleteOCIRegistry(oci.InstallOCIRegistryArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "OCI registry deleted."), nil
	})
}

func init() {
	register(toolname.EnsureOCIBuilder, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Oci().EnsureOciBuilder(oci.EnsureOciBuilderArgs{Builder: stringField(args, "builder")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Host OCI image builder is available."), nil
	})
}

func init() {
	register(toolname.ConfigureOCIStorage, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Oci().ConfigureOciStorage(ctx, oci.ConfigureOciStorageArgs{
			Runtime:       stringField(args, "runtime"),
			MaxBytes:      optionalInt64Field(args, "maxBytes"),
			MinAgeSeconds: optionalInt64Field(args, "minAgeSeconds"),
			PruneNow:      boolField(args, "pruneNow"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "OCI image storage retention policy applied."), nil
	})
}

func init() {
	register(toolname.InspectContainerStorage, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Oci().InspectContainerStorage(ctx, oci.InspectContainerStorageArgs{
			Runtime: stringField(args, "runtime"),
		})
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Container runtime storage usage inspected."), nil
	})
}

func init() {
	register(toolname.CleanupContainerStorage, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Oci().CleanupContainerStorage(ctx, oci.CleanupContainerStorageArgs{
			Runtime:       stringField(args, "runtime"),
			MaxBytes:      optionalInt64Field(args, "maxBytes"),
			MinAgeSeconds: optionalInt64Field(args, "minAgeSeconds"),
			DryRun:        boolField(args, "dryRun"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Container runtime storage cleanup completed."), nil
	})
}

func init() {
	register(toolname.BuildAndPushOCIImage, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Oci().BuildAndPushOciImage(ctx, oci.BuildAndPushOciImageArgs{
			ContextDir:       stringField(args, "contextDir"),
			Dockerfile:       stringField(args, "dockerfile"),
			Image:            stringField(args, "image"),
			Builder:          stringField(args, "builder"),
			InsecureRegistry: boolField(args, "insecureRegistry"),
			Platform:         stringField(args, "platform"),
			BuildArgs:        stringMapField(args, "buildArgs"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Built and pushed %s.", out["image"])), nil
	})
}

func init() {
	register(toolname.StageBuildContext, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		files := map[string]string{}
		if raw, ok := args["files"].(map[string]any); ok {
			for key, value := range raw {
				if text, ok := value.(string); ok {
					files[key] = text
				}
			}
		}
		out, err := svc.Oci().StageBuildContext(oci.StageBuildContextArgs{
			DestDir:      stringField(args, "destDir"),
			Files:        files,
			FileEncoding: stringField(args, "fileEncoding"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("Staged %v files into build context.", out["fileCount"])), nil
	})
}
