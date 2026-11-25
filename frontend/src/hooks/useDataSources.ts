import { useLiveInvalidate } from '@/hooks/useLiveInvalidate'
import { useWebSocketConnected } from '@/hooks/useWebSocket'
import { allTelemetryPresent, featureVisibility, telemetryPresence } from '@/lib/dataSources'
import { queryKeys, useHTTPCapturesStored, useStatus } from '@/lib/queries'
import { useTelemetryStore } from '@/stores/telemetryStore'

// Polling of the status while a source has no data and the WebSocket is
// disconnected; connected, the real-time notifications tell
const STATUS_POLL_MS = 15_000

// The status's counts come from stats cached for 5 s and, once expired,
// served for up to a minute while a refresh runs: an answer may predate
// stored records (after a reload), and so may the next one, which only
// starts the refresh when the cache was still fresh. The third answer, 12 s
// after the first, counts everything stored before the first.
const STATUS_RECHECK_MS = 6_000
const STATUS_RECHECKS = 2

const NO_LIVE = { metrics: 0, logs: 0, spans: 0 }

/**
 * The data sources the server has data from, for the menu and the home
 * page: the status, which telemetry is stored, and which menu entries to
 * show. Records arriving show up at once through the real-time
 * notifications; while a source has no data and the WebSocket is
 * disconnected, the status is polled instead.
 */
export function useDataSources() {
  const status = useStatus({
    refetchInterval: (query) =>
      query.state.dataUpdateCount <= STATUS_RECHECKS && !allTelemetryPresent(telemetryPresence(query.state.data, NO_LIVE)) ? STATUS_RECHECK_MS : false,
  })
  const metrics = useTelemetryStore((s) => s.metricsUpdateCount)
  const logs = useTelemetryStore((s) => s.logsUpdateCount)
  const spans = useTelemetryStore((s) => s.spansUpdateCount)
  const presence = telemetryPresence(status.data, { metrics, logs, spans })

  const proxyEnabled = status.data?.httpInspector?.enabled
  // Captures stored by an earlier start with the proxy on
  const captures = useHTTPCapturesStored({ enabled: proxyEnabled === false })
  const capturesStored = captures.isSuccess ? captures.data : undefined

  const connected = useWebSocketConnected()
  useLiveInvalidate({
    queryKey: queryKeys.status,
    enabled: !connected && !allTelemetryPresent(presence),
    intervalMs: STATUS_POLL_MS,
    // Notifications already count through the store's counters
    updateCount: 0,
    cancelRefetch: false,
  })

  return {
    status,
    presence,
    capturesStored,
    features: featureVisibility(presence, proxyEnabled, capturesStored),
  }
}
