package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wunderous/host-agents/internal/contract/toolname"
	"github.com/wunderous/host-agents/internal/domain/kubernetes"
	"github.com/wunderous/host-agents/internal/hostagent"
	"github.com/wunderous/host-agents/internal/resource"
)

func init() {
	register(toolname.ListKubernetesClusters, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Kubernetes().ListKubernetesClusters(stringField(args, "source"))
		if err != nil {
			return nil, err
		}
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.InstallHelmChart, EffectMutation, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := installHelmChartArgs(args, binding)
		out, err := svc.Kubernetes().InstallHelmChart(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("HelmChart '%s' deployment initiated.", parsed.ReleaseName)), nil
	})
}

func init() {
	register(toolname.UninstallHelmChart, EffectDestructive, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		parsed := uninstallHelmChartArgs(args, binding)
		out, err := svc.Kubernetes().UninstallHelmChart(parsed, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, fmt.Sprintf("HelmChart '%s' deleted.", parsed.ReleaseName)), nil
	})
}

func init() {
	register(toolname.ApplyManifest, EffectMutation, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Kubernetes().ApplyManifest(kubernetes.ApplyManifestArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Manifest: stringField(args, "manifest")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes manifest applied."), nil
	})
}

func init() {
	register(toolname.PutK8sSecret, EffectCredential, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		data := map[string]string{}
		if raw, ok := args["data"].(map[string]any); ok {
			for key, value := range raw {
				if text, ok := value.(string); ok {
					data[key] = text
				}
			}
		}
		out, err := svc.Kubernetes().PutK8sSecret(kubernetes.PutK8sSecretArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Name: stringField(args, "name"), Data: data}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Kubernetes Secret configured."), nil
	})
}

func init() {
	register(toolname.GetK8sResource, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Kubernetes().GetK8sResource(kubernetes.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceKind: stringField(args, "resourceKind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.DeleteK8sResource, EffectDestructive, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Kubernetes().DeleteK8sResource(kubernetes.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")}, onData)
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, "Kubernetes resource deleted."), nil
	})
}

func init() {
	register(toolname.GetK8sResourceStatus, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Kubernetes().GetK8sResourceStatus(kubernetes.K8sResourceArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Kind: stringField(args, "kind"), ResourceKind: stringField(args, "resourceKind"), ResourceName: stringField(args, "resourceName"), Namespace: stringField(args, "namespace")})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		if _, ok := out["message"]; !ok {
			out["message"] = "Kubernetes resource status returned."
		}
		return structuredResult(out, "Kubernetes resource status returned."), nil
	})
}

func init() {
	register(toolname.ListK8sEvents, EffectRead, resource.ClassControl, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		limit, _ := args["limit"].(float64)
		out, err := svc.Kubernetes().ListK8sEvents(kubernetes.K8sEventsArgs{URI: resourceURIFromBinding(binding), VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), Limit: int(limit)})
		if err != nil {
			return nil, err
		}
		out = withBindingURI(out, binding, "cluster")
		return structuredResult(out, ""), nil
	})
}

func init() {
	register(toolname.ConfigureServiceDomain, EffectMutation, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Kubernetes().ConfigureServiceDomain(kubernetes.ConfigureServiceDomainArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), IngressName: stringField(args, "ingressName"), Hostname: stringField(args, "hostname"), ServiceName: stringField(args, "serviceName"), ServicePort: intField(args, "servicePort"), IngressClass: stringField(args, "ingressClass")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Service domain mapping configured."), nil
	})
}

func init() {
	register(toolname.RemoveServiceDomain, EffectDestructive, resource.ClassNormal, TaskAware, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Kubernetes().RemoveServiceDomain(kubernetes.ConfigureServiceDomainArgs{VMName: vmNameFromBinding(binding), Namespace: stringField(args, "namespace"), IngressName: stringField(args, "ingressName")}, onData)
		if err != nil {
			return nil, err
		}
		return structuredResult(out, "Service domain mapping removed."), nil
	})
}

func init() {
	register(toolname.RenderHelmTemplate, EffectRead, resource.ClassNormal, TaskInline, func(ctx context.Context, svc *hostagent.Service, args map[string]any, binding ExecutionBinding, onData func(string)) (*mcp.CallToolResult, error) {
		out, err := svc.Kubernetes().RenderHelmTemplate(kubernetes.RenderHelmTemplateArgs{
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
