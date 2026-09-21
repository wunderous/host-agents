# opute-io-e2e-guard

## Requirement: Fail-closed production surface check

Automated guard verifies *.opute.io: DNS, TLS, HTTPS, negative path, ha-a continuity.
Exits non-zero on failure; required before commit.
