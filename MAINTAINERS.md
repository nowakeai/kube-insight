# Maintainers

This file records project ownership expectations. Add named maintainers before
the first public release.

## Current Owner

- Repository owner: `nowakeai`

## Maintainer Responsibilities

- Keep CI green on `main`.
- Review schema, storage, ingestion, and security-sensitive changes carefully.
- Verify that filters run before retained hashing and storage.
- Verify destructive filters record auditable decisions.
- Verify delete observations are preserved.
- Review release artifacts and GHCR images before public announcements.
- Triage security reports privately according to [SECURITY.md](SECURITY.md).

## Release Approval

A release should not be tagged until:

- tests and validation pass
- release notes are reviewed
- license selection is committed
- sensitive-data scans pass
- public security contact is configured
