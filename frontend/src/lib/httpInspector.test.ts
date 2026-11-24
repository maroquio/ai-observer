import { describe, it, expect } from 'vitest'
import {
  backLabel,
  clipText,
  exchangeFiltersFromValues,
  exchangePath,
  exchangeTokens,
  formatMaybeCount,
  formatShare,
  hasExchangeFilters,
  hasRedaction,
  isErrorExchange,
  linkQuality,
  linkQualityCounts,
  prettyJSON,
  runPath,
  sessionTrafficPath,
  EXCHANGE_FILTER_DEFAULTS,
} from './httpInspector'
import { exchangeFixture, usageFixture } from '@/test/httpInspectorFixtures'

describe('httpInspector helpers', () => {
  it('builds the routes, encoding every segment', () => {
    expect(runPath('run 1')).toBe('/http-inspector/runs/run%201')
    expect(sessionTrafficPath('a/b')).toBe('/sessions/a%2Fb?view=traffic')
    expect(exchangePath({ sessionId: 's1', runId: 'r1' }, 'e/1')).toBe('/sessions/s1/http-exchanges/e%2F1')
    expect(exchangePath({ runId: 'r1' }, 'e1')).toBe('/http-inspector/runs/r1/exchanges/e1')
  })

  it('rates how an exchange is linked to its session', () => {
    expect(linkQuality({ sessionId: 's', sessionEvidence: 'header:X-Claude-Code-Session-Id' })).toBe('header')
    expect(linkQuality({ sessionId: 's', sessionEvidence: 'body:metadata.user_id' })).toBe('metadata')
    expect(linkQuality({ sessionId: 's' })).toBe('metadata')
    expect(linkQuality({}, 'launcher')).toBe('launcher')
    expect(linkQuality({}, 'manual')).toBe('none')
    expect(linkQuality({})).toBe('none')

    const counts = linkQualityCounts(
      [exchangeFixture(), exchangeFixture({ runId: 'r2', sessionId: undefined }), exchangeFixture({ runId: 'r3', sessionId: undefined })],
      [{ id: 'r2', mode: 'launcher' }, { id: 'r3', mode: 'manual' }],
    )
    expect(counts).toEqual({ header: 1, metadata: 0, launcher: 1, none: 1 })
  })

  it('keeps absent values apart from zero', () => {
    expect(formatMaybeCount(undefined)).toBeNull()
    expect(formatMaybeCount(null)).toBeNull()
    expect(formatMaybeCount(0)).toBe('0')
    expect(formatShare(0)).toBe('0.0%')
    expect(formatShare(undefined)).toBeNull()
    expect(formatShare(0.4567)).toBe('45.7%')
  })

  it('reads the tokens of an exchange, marking count_tokens estimates', () => {
    expect(exchangeTokens(exchangeFixture())).toEqual({ value: 20_352, estimate: false })
    expect(exchangeTokens(exchangeFixture({ usage: usageFixture({ countTokens: 900 }) }))).toEqual({ value: 900, estimate: true })
    expect(exchangeTokens(exchangeFixture({ usage: undefined }))).toBeNull()
    expect(exchangeTokens(exchangeFixture({ usage: usageFixture({ totals: { complete: false } }) }))).toBeNull()
  })

  it('tells error exchanges', () => {
    expect(isErrorExchange({ status: 200, kind: 'model_inference', state: 'complete' })).toBe(false)
    expect(isErrorExchange({ status: 429, kind: 'model_inference', state: 'complete' })).toBe(true)
    expect(isErrorExchange({ status: 0, kind: 'transport_error', state: 'failed' })).toBe(true)
    expect(isErrorExchange({ status: 429, kind: 'quota_probe', state: 'complete' })).toBe(false)
  })

  it('leaves out filter values the API would reject', () => {
    expect(exchangeFiltersFromValues({ kind: 'token_count', status: '529', state: 'partial', search: '' }, '  rate ')).toEqual({
      kind: 'token_count',
      status: '529',
      state: 'partial',
      search: 'rate',
    })
    expect(exchangeFiltersFromValues({ kind: 'bogus', status: '99', state: 'nope', search: '' }, ' ')).toEqual({
      kind: undefined,
      status: undefined,
      state: undefined,
      search: undefined,
    })
    expect(hasExchangeFilters(EXCHANGE_FILTER_DEFAULTS)).toBe(false)
    expect(hasExchangeFilters({ ...EXCHANGE_FILTER_DEFAULTS, search: '  ' })).toBe(false)
    expect(hasExchangeFilters({ ...EXCHANGE_FILTER_DEFAULTS, status: 'error' })).toBe(true)
  })

  it('indents JSON and leaves other text as it is', () => {
    expect(prettyJSON('{"a":1}')).toEqual({ text: '{\n  "a": 1\n}', json: true })
    expect(prettyJSON('{not json')).toEqual({ text: '{not json', json: false })
    expect(prettyJSON('<b>x</b>')).toEqual({ text: '<b>x</b>', json: false })
  })

  it('finds redaction markers and clips long text', () => {
    expect(hasRedaction('key=[REDACTED:anthropic_key]')).toBe(true)
    expect(hasRedaction('[REDACTED]')).toBe(false)
    expect(hasRedaction(undefined)).toBe(false)
    expect(clipText('abcdef', 4)).toEqual({ text: 'abcd', clipped: true })
    expect(clipText('abc', 4)).toEqual({ text: 'abc', clipped: false })
  })

  it('labels the Back button by where it leads', () => {
    expect(backLabel('/sessions/s1?view=traffic')).toBe('Voltar à sessão')
    expect(backLabel('/sessions?service=claude-code')).toBe('Voltar às sessões')
    expect(backLabel('/http-inspector/runs/r1')).toBe('Voltar à execução')
    expect(backLabel('/http-inspector?mode=manual')).toBe('Voltar ao HTTP Inspector')
    expect(backLabel('/logs')).toBe('Voltar')
  })
})
