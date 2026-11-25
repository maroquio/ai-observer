import { describe, expect, it } from 'vitest'
import { allTelemetryPresent, featureVisibility, hasTelemetry, telemetryPresence } from './dataSources'
import { statusFixture } from '@/test/statusFixture'

const NO_LIVE = { metrics: 0, logs: 0, spans: 0 }

describe('telemetryPresence', () => {
  it('is unknown without a status or without counts', () => {
    expect(telemetryPresence(undefined, NO_LIVE)).toBeUndefined()
    expect(telemetryPresence({ version: 'v1', mode: 'serve' }, NO_LIVE)).toBeUndefined()
  })

  it('reads the stored counts', () => {
    const status = statusFixture({ telemetry: { metricCount: 3, logCount: 0, spanCount: 0 } })
    expect(telemetryPresence(status, NO_LIVE)).toEqual({ metrics: true, logs: false, spans: false })
  })

  it('counts real-time notifications, which arrive before the cached counts change', () => {
    const status = statusFixture()
    expect(telemetryPresence(status, NO_LIVE)).toEqual({ metrics: false, logs: false, spans: false })
    expect(telemetryPresence(status, { metrics: 0, logs: 2, spans: 1 })).toEqual({ metrics: false, logs: true, spans: true })
  })
})

describe('featureVisibility', () => {
  it('shows every page while the sources are unknown', () => {
    expect(featureVisibility(undefined, undefined, undefined)).toEqual({
      dashboards: true, metrics: true, logs: true, traces: true, sessions: true, httpInspector: true,
    })
  })

  it('shows only the HTTP Inspector when only the proxy is on', () => {
    const none = { metrics: false, logs: false, spans: false }
    expect(featureVisibility(none, true, undefined)).toEqual({
      dashboards: false, metrics: false, logs: false, traces: false, sessions: false, httpInspector: true,
    })
  })

  it('shows the pages of the telemetry Claude Code sends (metrics and logs, no spans)', () => {
    const claude = { metrics: true, logs: true, spans: false }
    expect(featureVisibility(claude, false, false)).toEqual({
      dashboards: true, metrics: true, logs: true, traces: false, sessions: true, httpInspector: false,
    })
  })

  it('shows sessions for spans alone, and the dashboards for any telemetry', () => {
    const spansOnly = { metrics: false, logs: false, spans: true }
    const visible = featureVisibility(spansOnly, false, false)
    expect(visible.sessions).toBe(true)
    expect(visible.traces).toBe(true)
    expect(visible.dashboards).toBe(true)
    expect(visible.metrics).toBe(false)
  })

  it('keeps the HTTP Inspector with the proxy off while captures are stored or not known', () => {
    const none = { metrics: false, logs: false, spans: false }
    expect(featureVisibility(none, false, true).httpInspector).toBe(true)
    expect(featureVisibility(none, false, undefined).httpInspector).toBe(true)
    expect(featureVisibility(none, false, false).httpInspector).toBe(false)
  })
})

describe('hasTelemetry and allTelemetryPresent', () => {
  it('tell any from all', () => {
    expect(hasTelemetry({ metrics: false, logs: false, spans: false })).toBe(false)
    expect(hasTelemetry({ metrics: false, logs: true, spans: false })).toBe(true)
    expect(allTelemetryPresent({ metrics: true, logs: true, spans: false })).toBe(false)
    expect(allTelemetryPresent({ metrics: true, logs: true, spans: true })).toBe(true)
    expect(allTelemetryPresent(undefined)).toBe(true)
  })
})
