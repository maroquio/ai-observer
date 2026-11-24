import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { queryKeys } from '@/lib/queries'
import type { UpdateHTTPCaptureRunRequest } from '@/types/httpInspector'

// Changes of HTTP Inspector capture runs. Never retried (the server may have
// applied them); the cache is refreshed from the server afterwards, since
// PUT answers the run without its summary.

/** Renames a run or sets its note (omitted fields stay) */
export function useUpdateHTTPCaptureRun(runId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (req: UpdateHTTPCaptureRunRequest) => api.updateHTTPCaptureRun(runId, req),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.httpInspector.runDetail(runId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.httpInspector.runs }),
        queryClient.invalidateQueries({ queryKey: queryKeys.httpInspector.sessions }),
      ]),
  })
}

/** Deletes a finished run with its stored bodies (the server refuses an active one: 409) */
export function useDeleteHTTPCaptureRun() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (runId: string) => api.deleteHTTPCaptureRun(runId),
    onSuccess: (_data, runId) => {
      queryClient.removeQueries({ queryKey: queryKeys.httpInspector.run(runId) })
      void queryClient.invalidateQueries({ queryKey: queryKeys.httpInspector.runs })
      void queryClient.invalidateQueries({ queryKey: queryKeys.httpInspector.sessions })
      void queryClient.invalidateQueries({ queryKey: queryKeys.sessions.lists })
      void queryClient.invalidateQueries({ queryKey: queryKeys.httpInspector.status })
    },
  })
}
