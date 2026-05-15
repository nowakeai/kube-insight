## Summary

What changed and why?

## Validation

- [ ] `make test`
- [ ] `make build`
- [ ] `make validate` when ingestion, storage, query, API, or MCP behavior changed
- [ ] `git diff --check`

## Safety

- [ ] No kubeconfig, bearer token, generated SQLite database, or unsanitized cluster payload is committed
- [ ] Behavior changes are documented
- [ ] Delete observations are preserved when relevant
- [ ] Destructive filters remain auditable when relevant

## Notes for reviewers

Call out schema changes, migration expectations, query-plan risks, or release
impacts.
