package hostmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	providercontract "github.com/wunderous/host-agents/contracts/provider"
	hostcapability "github.com/wunderous/host-agents/internal/capability"
	"github.com/wunderous/host-agents/internal/resource"
	"github.com/wunderous/host-agents/internal/tools"
)

// providerOperationDescriptor is the one projection from a provider manifest
// operation to the host capability catalog. Candidate validation and active
// registration must use the same descriptor so a recipe cannot validate one
// schema and execute another.
func providerOperationDescriptor(
	manifest providercontract.InstallManifest,
	service providercontract.ServiceDefinition,
	operation providercontract.Operation,
	generationID string,
) tools.CapabilityDescriptor {
	version := operation.Version
	if version == 0 {
		version = 1
	}
	description := strings.TrimSpace(operation.Description)
	if description == "" {
		description = "Provider service " + service.ID + " operation " + operation.ID
	}
	descriptor := tools.CapabilityDescriptor{
		OperationID:       operation.ID,
		Version:           version,
		Name:              operation.ID,
		Description:       description,
		InputSchema:       operation.InputSchema,
		OutputSchema:      operation.OutputSchema,
		OutputType:        operation.OutputType,
		ResultTypes:       operation.ResultTypes,
		Effect:            operation.Effect,
		Privilege:         operation.Effect,
		RequiresApproval:  operation.Effect != "read",
		Provider:          manifest.Provider.ID,
		Implementation:    "provider:" + manifest.Provider.ID,
		GenerationID:      generationID,
		ResourceKinds:     append([]string(nil), operation.ResourceKinds...),
		RequiredFields:    requiredSchemaFields(operation.InputSchema),
		ValidationSchema:  operation.ValidationSchema,
		ObservationSchema: firstNonEmpty(operation.ObservationSchema, hostcapability.ObservationSchemaVersion),
		Requires:          providerBindings(operation.Requires),
		Produces:          providerBindings(operation.Produces),
		Idempotent:        operation.Idempotent,
		SupportsReadiness: operation.SupportsReadiness,
	}
	if operation.ResourceCost != nil {
		descriptor.ResourceCost = &tools.ResourceCost{
			CPUCores: operation.ResourceCost.CPUCores, MemoryBytes: operation.ResourceCost.MemoryBytes,
			DiskBytes: operation.ResourceCost.DiskBytes, Tasks: operation.ResourceCost.Tasks,
			Class: operation.ResourceCost.Class,
		}
	}
	return descriptor
}

// newProviderOperationCapability creates the provider adapter for one
// operation. Candidate capabilities are deliberately not registered in the
// public catalog: they can only be reached by the candidate's own durable
// recipe run. The generation session and candidate adapter are still checked
// at invocation time, preserving the same fail-closed affinity as active
// provider operations.
func (s *Server) newProviderOperationCapability(
	manifest providercontract.InstallManifest,
	service providercontract.ServiceDefinition,
	operation providercontract.Operation,
	generationID string,
	candidate bool,
) hostcapability.Capability {
	descriptor := providerOperationDescriptor(manifest, service, operation, generationID)
	return hostcapability.NewProviderAdapter(descriptor, func(ctx context.Context, args hostcapability.RawArguments, _ tools.ExecutionBinding, _ hostcapability.ExecutionSink) (*mcp.CallToolResult, error) {
		if candidate {
			session, err := s.providerLifecycle.OpenSessionForGeneration(manifest.Provider.ID, generationID)
			if err != nil {
				return tools.ErrorResult(err), nil
			}
			defer session.Close()
			s.providerMu.RLock()
			adapter := s.providerCandidates[generationID]
			s.providerMu.RUnlock()
			if adapter == nil {
				return tools.ErrorResult(fmt.Errorf("provider generation %q is not connected", generationID)), nil
			}
			return adapter.CallSynchronousOnly(ctx, operation.ID, args)
		}

		session, err := s.providerLifecycle.OpenSession(manifest.Provider.ID)
		if err != nil || session.GenerationID() != descriptor.GenerationID {
			if err == nil {
				session.Close()
			}
			return tools.ErrorResult(fmt.Errorf("provider generation %q is no longer active", descriptor.GenerationID)), nil
		}
		defer session.Close()
		value, ok := s.providerServiceValueFor(manifest.Provider.ID, service.ID, session.GenerationID())
		if !ok || value.adapter == nil {
			return tools.ErrorResult(fmt.Errorf("provider generation %q is not connected", descriptor.GenerationID)), nil
		}
		return value.adapter.CallSynchronousOnly(ctx, operation.ID, args)
	})
}

func (s *Server) providerCapabilitiesForGeneration(manifest providercontract.InstallManifest, generationID string, candidate bool) ([]hostcapability.Capability, []tools.CapabilityDescriptor) {
	capabilities := make([]hostcapability.Capability, 0)
	descriptors := make([]tools.CapabilityDescriptor, 0)
	for _, service := range manifest.Services {
		for _, operation := range service.Operations {
			descriptor := providerOperationDescriptor(manifest, service, operation, generationID)
			capabilities = append(capabilities, s.newProviderOperationCapability(manifest, service, operation, generationID, candidate))
			descriptors = append(descriptors, descriptor)
		}
	}
	return capabilities, descriptors
}

// providerCandidateSnapshot projects a connected candidate into a private
// validation snapshot. It does not mutate the registry or MCP tools/list, so
// an unactivated provider generation cannot be invoked by another caller.
func (s *Server) providerCandidateSnapshot(generationID string) (tools.CapabilityCatalogSnapshot, bool) {
	if strings.TrimSpace(generationID) == "" {
		return tools.CapabilityCatalogSnapshot{}, false
	}
	s.providerMu.RLock()
	manifest, manifestOK := s.providerCandidateManifests[generationID]
	_, adapterOK := s.providerCandidates[generationID]
	s.providerMu.RUnlock()
	if !manifestOK || !adapterOK {
		return tools.CapabilityCatalogSnapshot{}, false
	}
	active := s.CatalogSnapshot()
	_, descriptors := s.providerCapabilitiesForGeneration(manifest, generationID, true)
	merged := make([]tools.CapabilityDescriptor, 0, len(active.Tools)+len(descriptors))
	for _, descriptor := range active.Tools {
		if descriptor.Provider != manifest.Provider.ID {
			merged = append(merged, descriptor)
		}
	}
	merged = append(merged, descriptors...)
	return tools.BuildCapabilityCatalogFromDescriptors(active.ProviderID, merged), true
}

func (s *Server) providerCandidateCapability(generationID, name string) (hostcapability.Capability, bool) {
	s.providerMu.RLock()
	manifest, manifestOK := s.providerCandidateManifests[generationID]
	s.providerMu.RUnlock()
	if !manifestOK {
		return nil, false
	}
	capabilities, descriptors := s.providerCapabilitiesForGeneration(manifest, generationID, true)
	for index, descriptor := range descriptors {
		if descriptor.Name == name {
			return capabilities[index], true
		}
	}
	return nil, false
}

// dispatchCandidateTool is the private execution seam for the candidate's
// setup recipe. It shares admission, binding, result validation, and durable
// invocation evidence with ordinary dispatch while keeping candidate
// capabilities out of the public registry until activation.
func (s *Server) dispatchCandidateTool(ctx context.Context, name string, args map[string]any, onData func(string), generationID string, snapshot tools.CapabilityCatalogSnapshot) (*mcp.CallToolResult, error, bool) {
	capabilityValue, ok := s.providerCandidateCapability(generationID, name)
	if !ok {
		return nil, nil, false
	}
	binding, err := resolveExecutionBindingWithSnapshot(s, name, args, snapshot)
	if err != nil {
		return tools.ErrorResult(err), nil, true
	}
	if s.admission != nil {
		reservation, err := s.admitInvocationWithDescriptor(ctx, name, args, binding, capabilityValue.Definition(), true, true)
		if err != nil {
			return tools.ErrorResult(err), nil, true
		}
		if reservation != nil {
			defer func() { _ = s.admission.Release(reservation) }()
			binding.ReservationID = reservation.ID
			binding.ResourcePolicyRevision = s.admission.Snapshot().PolicyRevision
			ctx = resource.WithReservation(ctx, reservation)
		}
	}
	result, err := s.dispatchCapabilityValue(ctx, name, args, binding, onData, capabilityValue)
	return result, err, true
}
