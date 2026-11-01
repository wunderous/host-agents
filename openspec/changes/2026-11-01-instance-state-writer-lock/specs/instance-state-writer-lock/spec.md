# instance-state-writer-lock Specification

## Purpose

One Host Agent process owns an instance's durable state at a time.

## ADDED Requirements

### Requirement: An agent locks its instance state before opening it

The Host Agent MUST take an exclusive, non-blocking lock on `host-agent.lock`
in its instance root, and in its standalone state and SQLite database roots
when they resolve outside the instance root, in that order, after
configuration validation and before it opens any store or binds a listener.
It MUST hold the lock until the runtime closes.

#### Scenario: A second agent on the same instance is refused

- **WHEN** an agent starts on an instance root another process has locked
- **THEN** it exits 1 with one stderr line naming the absolute lock path
- **AND** it creates no store, row or listener

#### Scenario: A state root outside the instance root is locked too

- **WHEN** `OPUTE_STANDALONE_STATE_DIR` lies outside the instance root and
  another process holds its lock
- **THEN** the agent is refused, naming that root's lock file

#### Scenario: A crashed holder leaves no stale lock

- **WHEN** the process holding the lock is killed with SIGKILL
- **THEN** the next agent on that instance starts

### Requirement: Configuration check honours the instance lock

`--check` MUST take the same lock and MUST report the same refusal while
another process holds the instance.

#### Scenario: Check next to a running agent

- **WHEN** `--check` runs on an instance a running agent holds
- **THEN** it exits 1 with the in-use line and does not print
  `configuration ok`
