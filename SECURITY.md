# Security Policy

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Report it privately through the repository's GitHub Security Advisories page. Include the affected version, operating system, MCP client, provider configuration, reproduction steps, and impact. Remove credentials, tunnel tokens, hostnames, and other sensitive values from the report.

We will acknowledge reports within five business days and coordinate disclosure, remediation, and credit with the reporter.

The standalone agent executes infrastructure commands on the host where it runs. Treat MCP client prompts, tool arguments, environment variables, state files, and release artifacts as security-sensitive inputs.
