export type RetryBranchRun = {
  id: string
  createdAt?: string
  metadata?: unknown
}

export function displayRunIdsForRetryBranches<T extends RetryBranchRun>(runIds: string[], runsById: Record<string, T | undefined>) {
  const visible: string[] = []
  for (const runId of chronologicalRunIds(runIds, runsById)) {
    const run = runsById[runId]
    if (!run) continue
    const retryOf = retryOfRunId(run)
    if (!retryOf) {
      visible.push(run.id)
      continue
    }

    const rootId = retryRootRunId(run, runsById)
    const replaceIndex = visible.findIndex((candidateId) => {
      const candidate = runsById[candidateId]
      return candidate ? retryRootRunId(candidate, runsById) === rootId : false
    })
    if (replaceIndex >= 0) {
      visible.splice(replaceIndex, visible.length - replaceIndex, run.id)
    } else {
      visible.push(run.id)
    }
  }
  return visible
}

function chronologicalRunIds<T extends RetryBranchRun>(runIds: string[], runsById: Record<string, T | undefined>) {
  return runIds
    .map((id, index) => ({ id, index, createdAt: parseCreatedAt(runsById[id]?.createdAt) }))
    .sort((a, b) => {
      if (a.createdAt !== undefined && b.createdAt !== undefined && a.createdAt !== b.createdAt) return a.createdAt - b.createdAt
      return a.index - b.index
    })
    .map((item) => item.id)
}

function parseCreatedAt(value: string | undefined) {
  if (!value) return undefined
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : undefined
}

export function retryRootRunId<T extends RetryBranchRun>(run: T, runsById: Record<string, T | undefined>) {
  let root = run
  const seen = new Set<string>()
  while (!seen.has(root.id)) {
    seen.add(root.id)
    const retryOf = retryOfRunId(root)
    const parent = retryOf ? runsById[retryOf] : undefined
    if (!parent) break
    root = parent
  }
  return root.id
}

export function retryOfRunId(run: RetryBranchRun) {
  const metadata = run.metadata
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) return undefined
  const retryOf = (metadata as Record<string, unknown>).retryOfRunId
  return typeof retryOf === "string" && retryOf ? retryOf : undefined
}
