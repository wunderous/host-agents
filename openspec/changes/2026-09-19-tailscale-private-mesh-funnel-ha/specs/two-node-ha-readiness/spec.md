# two-node-ha-readiness Specification

## Purpose

Adds Tailscale mesh, datastore-mode, and public-ingress evidence to the
two-server readiness boundary. Two ready servers remain a membership fact, not
automatic consensus availability.

## ADDED Requirements

### Requirement: Two-server readiness names the datastore and availability mode

The readiness gate MUST receive an explicit datastore mode and MUST report the
availability claim that mode permits. It MUST fail closed when a caller asks
for generic HA without evidence that the datastore itself survives the stated
failure.

#### Scenario: Two servers use an independently HA external datastore

- **WHEN** both exact servers are ready, private mesh probes pass, and the
  external datastore's independent readiness and failover evidence pass
- **THEN** the gate may report control-plane write continuity as eligible
- **AND** evidence records the datastore identity, mode, generation, and
  endpoint binding

#### Scenario: Two servers use embedded two-member etcd

- **WHEN** both exact servers are ready but the datastore is embedded etcd with
  two voting members
- **THEN** the gate reports serving-continuity-only
- **AND** it does not claim automatic Kubernetes write availability after one
  member fails

### Requirement: Readiness proves private mesh paths separately from Funnel

The gate MUST probe required server-to-server paths over the admitted private
overlay and MUST probe public ingress separately. A public Funnel success MUST
NOT substitute for private mesh readiness.

#### Scenario: Mesh and Funnel both pass

- **WHEN** both directions of private API/control-plane/data-plane probes pass
  and the external public ingress probe passes
- **THEN** the evidence records separate private and public path results
- **AND** the readiness result identifies which path serves which purpose

#### Scenario: Funnel passes but mesh fails

- **WHEN** the public endpoint answers but one required private peer path fails
- **THEN** readiness remains BLOCKED
- **AND** no K3s join, public HA claim, or promotion is reported complete

### Requirement: Failure injection reports public and consensus axes separately

The failure gate MUST stop each server in turn and report public served-surface
continuity, durable application-store continuity, datastore/quorum state,
Kubernetes API write availability, and Funnel endpoint stability as separate
observations.

#### Scenario: One node stops in embedded two-member etcd mode

- **WHEN** either server is stopped
- **THEN** the served-surface result may pass if the surviving ingress serves
  the workload
- **AND** Kubernetes write availability is recorded as lost until acknowledged
  typed recovery
- **AND** Funnel stability is reported rather than inferred

#### Scenario: One node stops in external-datastore mode

- **WHEN** either server is stopped and the external datastore remains ready
- **THEN** the surviving server's control-plane write result is measured
- **AND** the result is not generalized beyond the observed datastore and
  topology evidence

### Requirement: Recovery and promotion are typed and acknowledged

Any embedded-etcd quorum recovery or node-specific Funnel promotion MUST be an
explicit typed operation with target identity, preconditions, acknowledgement
where irreversible, and durable redacted evidence. Liveness alone MUST NOT
trigger either operation automatically.

#### Scenario: Recovery is requested without its acknowledgement

- **WHEN** the caller omits the required single-member-reset acknowledgement
- **THEN** the recovery operation is rejected before mutation
- **AND** the cluster remains in its observed degraded state
