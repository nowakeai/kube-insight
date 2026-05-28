import { AgentAPIError } from "@/lib/agent-api"

export function shouldCreateReplacementRunForRetryError(error: unknown) {
  return error instanceof AgentAPIError && error.status === 404
}

export function retryReplacementMetadata(retryRunId: string) {
  return {
    retryOfRunId: retryRunId,
    retryFallback: "missing-original-run",
  }
}

export function retryErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Unknown error"
}
