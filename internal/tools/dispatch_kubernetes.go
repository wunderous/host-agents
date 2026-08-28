package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/ops"
)

func init() {
	register(toolname.ListKubernetesClusters, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ListKubernetesClusters(stringField(args, "source"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.InstallHelmChart, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := installHelmChartArgs(args, binding)
		out, err := svc.InstallHelmChart(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("HelmChart '%s' deployment initiated.", parsed.ReleaseName)), nil
	})
}

func init() {
	register(toolname.UninstallHelmChart, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := uninstallHelmChartArgs(args, binding)
		out, err := svc.UninstallHelmChart(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("HelmChart '%s' deleted.", parsed.ReleaseName)), nil
	})
}

func init() {
	register(toolname.ApplyManifest, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ApplyManifest(ops.ApplyManifestArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Manifest: stringField(args, "manifest")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes manifest applied."), nil
	})
}

func init() {
	register(toolname.PutK8sSecret, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		data := map[string]string{}
		if raw, ok := args["data"].(map[string]any); ok {
			for key, value := range raw {
				if text, ok := value.(string); ok {
					data[key] = text
				}
			}
		}
		out, err := svc.PutK8sSecret(ops.PutK8sSecretArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Data: data}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes Secret configured."), nil
	})
}

func init() {
	register(toolname.GetK8sResource, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.GetK8sResource(ops.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceKind: stringField(args, "resourceKind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.DeleteK8sResource, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.DeleteK8sResource(ops.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")}, onData)
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, "Kubernetes resource deleted."), nil
	})
}

func init() {
	register(toolname.GetK8sResourceStatus, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.GetK8sResourceStatus(ops.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceKind: stringField(args, "resourceKind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ListK8sEvents, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		limit, _ := args["limit"].(float64)
		out, err := svc.ListK8sEvents(ops.K8sEventsArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Limit: int(limit)})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ConfigureServiceDomain, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.ConfigureServiceDomain(ops.ConfigureServiceDomainArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), IngressName: stringField(args, "ingressName"), Hostname: stringField(args, "hostname"), ServiceName: stringField(args, "serviceName"), ServicePort: intField(args, "servicePort"), IngressClass: stringField(args, "ingressClass")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Service domain mapping configured."), nil
	})
}

func init() {
	register(toolname.RemoveServiceDomain, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.RemoveServiceDomain(ops.ConfigureServiceDomainArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), IngressName: stringField(args, "ingressName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Service domain mapping removed."), nil
	})
}

func init() {
	register(toolname.RenderHelmTemplate, func(ctx context.Context, svc *ops.HostOperationsService, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.RenderHelmTemplate(ops.RenderHelmTemplateArgs{
			ChartPath:   stringField(args, "chartPath"),
			ReleaseName: stringField(args, "releaseName"),
			ValuesFiles: stringSliceField(args, "valuesFiles"),
			Set:         stringSliceField(args, "set"),
			Namespace:   stringField(args, "namespace"),
		}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Helm chart rendered."), nil
	})
}
