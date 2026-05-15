# Security Policy

## Supported Versions

kube-insight is currently pre-1.0. Security fixes target the `main` branch until
the project starts publishing stable release lines.

## Reporting a Vulnerability

Please do not open a public issue for a suspected vulnerability.

Until a public security contact is configured for the repository, report issues
privately to the project maintainers through the repository owner's preferred
private channel. Include:

- affected commit or release
- reproduction steps
- expected impact
- whether real cluster data, credentials, or Secret payloads were exposed

## Security Scope

High-priority reports include:

- retained unsanitized Secret data
- kubeconfig or bearer-token exposure
- write-capable SQL through read-only query surfaces
- MCP/API behavior that bypasses intended read-only boundaries
- incorrect handling of delete observations that hides destructive events
- sanitizer bypasses for common Kubernetes sensitive fields

## Data Handling Expectations

kube-insight is designed to filter and sanitize data before retained hashing and
storage. Contributors should treat raw Kubernetes exports, local SQLite
databases, and generated validation output as sensitive unless explicitly
produced from sanitized fixtures.
