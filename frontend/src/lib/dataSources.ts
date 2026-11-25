import type { StatusResponse } from '@/types/status'

// The dashboard's data comes from three independent sources: OTLP
// telemetry (and session files, stored as the same logs and metrics), and
// the HTTP Inspector's capture proxy. The menu shows a page only when its
// source has data, so a server that only captures API traffic does not
// offer empty Metrics, Logs and Traces pages.

/** Which kinds of telemetry the server holds */
export interface TelemetryPresence {
  metrics: boolean
  logs: boolean
  spans: boolean
}

/** Real-time notifications this tab received (telemetryStore counters) */
export interface LiveTelemetryCounts {
  metrics: number
  logs: number
  spans: number
}

/** Menu entries that depend on a data source */
export type DataFeature = 'dashboards' | 'metrics' | 'logs' | 'traces' | 'sessions' | 'httpInspector'

export type FeatureVisibility = Record<DataFeature, boolean>

/**
 * What telemetry the server holds: its status's counts, or a real-time
 * notification of stored records (the status's counts are cached for a few
 * seconds, so the first records would otherwise show up only later).
 * undefined when unknown: no status, or one without counts.
 */
export function telemetryPresence(status: Partial<StatusResponse> | undefined, live: LiveTelemetryCounts): TelemetryPresence | undefined {
  const counts = status?.telemetry
  if (!counts) {
    return undefined
  }
  return {
    metrics: (counts.metricCount ?? 0) > 0 || live.metrics > 0,
    logs: (counts.logCount ?? 0) > 0 || live.logs > 0,
    spans: (counts.spanCount ?? 0) > 0 || live.spans > 0,
  }
}

/** Whether any telemetry is stored; also decides between the dashboard and the welcome page */
export function hasTelemetry(presence: TelemetryPresence): boolean {
  return presence.metrics || presence.logs || presence.spans
}

/**
 * Which menu entries to show. Unknown sources count as present, so a status
 * that failed or has not loaded never hides a page.
 *
 * - dashboards: any telemetry (the same rule as the home page)
 * - metrics, logs: their records; traces: spans; sessions: logs or spans
 * - httpInspector: the proxy runs, or captures are stored
 *   (capturesStored undefined: not known)
 */
export function featureVisibility(
  presence: TelemetryPresence | undefined,
  proxyEnabled: boolean | undefined,
  capturesStored: boolean | undefined,
): FeatureVisibility {
  const p = presence ?? { metrics: true, logs: true, spans: true }
  return {
    dashboards: hasTelemetry(p),
    metrics: p.metrics,
    logs: p.logs,
    traces: p.spans,
    sessions: p.logs || p.spans,
    httpInspector: proxyEnabled !== false || capturesStored !== false,
  }
}

/** Whether every telemetry source has data, so nothing waits for its first records */
export function allTelemetryPresent(presence: TelemetryPresence | undefined): boolean {
  return presence === undefined || (presence.metrics && presence.logs && presence.spans)
}
