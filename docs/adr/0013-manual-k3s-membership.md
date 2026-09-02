# ADR 0013: Manual K3s membership is provider-native and fail-closed

Status: accepted for the P1.5 implementation slice
Date: 2026-09-01

## Decision

The Host Agent publishes neutral K3s membership operation IDs and delegates
their concrete execution to the active provider generation. Native K3s node
membership, readiness, datastore mode, and the redacted cluster identity are
the authority for observations. Platform recipe state, a provider process, or
an MCP 200 response is not membership evidence.

The K3s provider refuses fresh-cluster initialization on `join-node`, requires
`cluster-init=false`, does not return raw tokens, and stops server removal when
the source-cluster quorum proof is not available. Join redemption is not
implemented as a provider-local shortcut: it belongs to an authenticated
source-to-destination Host Agent handshake. Stable HA endpoint creation belongs
to the selected data-plane overlay/tunneling provider.

## Consequences

The provider can safely expose preflight and native inspection before the
overlay and handshake exist. A recipe run must report `recovery_required` or a
typed preflight failure when those owners are absent. The two-node fixture in
the sibling Platform repository is explicitly an independent MCP coordinator
simulation and must not be labelled native K3s HA.

## Verification

The provider package tests cover operation registration, native membership
parsing, endpoint validation, and target/role safety. A future native HA gate
must additionally prove the K3s node list from the source target, the
embedded-etcd quorum, guest-originated data-plane reachability, and cleanup
through typed Host Agent lifecycle calls.
