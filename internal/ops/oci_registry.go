package ops

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wunderous/host-agents/internal/domain/kubernetes"
)

type InstallOCIRegistryArgs struct {
	VMName       string `json:"vmName"`
	Namespace    string `json:"namespace,omitempty"`
	Name         string `json:"name,omitempty"`
	Image        string `json:"image,omitempty"`
	StorageSize  string `json:"storageSize,omitempty"`
	StorageClass string `json:"storageClass,omitempty"`
	NodePort     int    `json:"nodePort,omitempty"`
}

func (s *HostOperationsService) DeleteOCIRegistry(args InstallOCIRegistryArgs, onData func(string)) (map[string]any, error) {
	namespace := defaultString(args.Namespace, "registry-system")
	if strings.TrimSpace(args.VMName) == "" {
		return nil, errors.New("vmName is required")
	}
	if err := kubernetes.ValidateIdentifier(namespace, "namespace"); err != nil {
		return nil, err
	}
	targetURI, err := s.kubernetesTargetURI(args.VMName)
	if err != nil {
		return nil, err
	}
	if _, err := s.DeleteK8sResource(K8sResourceArgs{URI: targetURI, Kind: "namespace", ResourceName: namespace}, onData); err != nil {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "namespace": namespace, "deleted": true}, nil
}

func (s *HostOperationsService) InstallOCIRegistry(args InstallOCIRegistryArgs, onData func(string)) (map[string]any, error) {
	vmName := strings.TrimSpace(args.VMName)
	if vmName == "" {
		return nil, errors.New("vmName is required")
	}
	namespace := defaultString(args.Namespace, "registry-system")
	name := defaultString(args.Name, "local-registry")
	image := defaultString(args.Image, "registry:3")
	storageSize := defaultString(args.StorageSize, "20Gi")
	storageClass := defaultString(args.StorageClass, "local-path")
	if err := kubernetes.ValidateIdentifier(namespace, "namespace"); err != nil {
		return nil, err
	}
	if err := kubernetes.ValidateIdentifier(name, "name"); err != nil {
		return nil, err
	}
	if strings.ContainsAny(image, "\r\n'") || strings.TrimSpace(image) == "" {
		return nil, errors.New("image is invalid")
	}
	if strings.ContainsAny(storageSize, "\r\n'") || strings.TrimSpace(storageSize) == "" {
		return nil, errors.New("storageSize is invalid")
	}
	if strings.ContainsAny(storageClass, "\r\n'") || strings.TrimSpace(storageClass) == "" {
		return nil, errors.New("storageClass is invalid")
	}
	nodePort := args.NodePort
	if nodePort == 0 {
		nodePort = 30500
	}
	if nodePort < 30000 || nodePort > 32767 {
		return nil, errors.New("nodePort must be between 30000 and 32767")
	}
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s-data
  namespace: %s
spec:
  accessModes: ["ReadWriteOnce"]
  storageClassName: %s
  resources:
    requests:
      storage: %s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: %s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %s
    spec:
      containers:
        - name: registry
          image: %s
          ports:
            - name: registry
              containerPort: 5000
          volumeMounts:
            - name: data
              mountPath: /var/lib/registry
          readinessProbe:
            httpGet:
              path: /v2/
              port: registry
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: %s-data
---
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  type: NodePort
  selector:
    app.kubernetes.io/name: %s
  ports:
    - name: registry
      port: 5000
      targetPort: registry
      nodePort: %d
`, namespace, name, namespace, storageClass, storageSize, name, namespace, name, name, image, name, name, namespace, name, nodePort)
	targetURI, err := s.kubernetesTargetURI(vmName)
	if err != nil {
		return nil, err
	}
	out, err := s.ApplyManifest(ApplyManifestArgs{URI: targetURI, Manifest: manifest}, onData)
	if err != nil {
		return nil, err
	}
	out["namespace"] = namespace
	out["name"] = name
	out["nodePort"] = nodePort
	out["endpointHint"] = fmt.Sprintf("<vm-ip>:%d", nodePort)
	return out, nil
}

func (s *HostOperationsService) GetOCIRegistryStatus(args InstallOCIRegistryArgs) (map[string]any, error) {
	namespace := defaultString(args.Namespace, "registry-system")
	name := defaultString(args.Name, "local-registry")
	targetURI, err := s.kubernetesTargetURI(args.VMName)
	if err != nil {
		return nil, err
	}
	deployment, err := s.GetK8sResource(K8sResourceArgs{URI: targetURI, Kind: "deployment", ResourceName: name, Namespace: namespace})
	if err != nil {
		return nil, err
	}
	return map[string]any{"vmName": args.VMName, "namespace": namespace, "name": name, "status": "installed", "deployment": deployment["resource"]}, nil
}
