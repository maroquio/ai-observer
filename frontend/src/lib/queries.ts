import { useEffect } from 'react'
import { keepPreviousData, useInfiniteQuery, useQueries, useQuery, useQueryClient, type UseQueryOptions } from '@tanstack/react-query'
import { api, type HTTPExchangeQuery, type MetricQuery } from '@/lib/api'
import type { DashboardsResponse, DashboardWithWidgets } from '@/types/dashboard'
import type { StatusResponse } from '@/types/status'
import { resolveRangeParams, type RangeSource, type TimeSelectionKey } from '@/lib/timeUtils'
import { useProviderStore } from '@/stores/providerStore'
import type { TraceKind } from '@/types/traces'
import type { HistogramStat } from '@/types/metrics'
import type { HTTPBodyPart, HTTPCaptureRunMode, HTTPCaptureRunState, HTTPToolLink } from '@/types/httpInspector'

// Query keys and hooks of every read endpoint. Keys are arrays starting with
// the resource, so queryClient.invalidateQueries({ queryKey: queryKeys.logs.all })
// refreshes every logs query. Keys hold the time selection's identity
// (RangeSource.key: timeframe or absolute bounds), never the moving bounds
// of a relative window: queryFns resolve those when they run.

// Filters of the list endpoints; undefined and '' mean "not filtered"
export interface TraceFilters {
  service?: string
  search?: string
  background?: boolean
}

export interface LogFilters {
  service?: string
  severity?: string
  traceId?: string
  search?: string
}

export interface SessionFilters {
  service?: string
  // Words and "quoted phrases" one record of the session must all contain
  search?: string
}

// Filters of the HTTP Inspector's exchange lists (HTTPExchangeQuery without
// the page)
export type HTTPExchangeFilters = Omit<HTTPExchangeQuery, 'limit' | 'cursor'>

export interface HTTPRunFilters {
  mode?: HTTPCaptureRunMode
  state?: HTTPCaptureRunState
}

export interface MetricPointFilters {
  service?: string
  name?: string
  type?: string
}

// A page of a cursor-paginated list (usePagination provides both)
export interface PageParams {
  limit: number
  cursor?: string
}

export interface MetricSeriesParams {
  name: string
  service?: string
  intervalSeconds?: number
  aggregate?: boolean
  // Attribute keys the series are grouped by (default: type)
  groupBy?: string[]
  // Exact attribute values the points must have
  filters?: Record<string, string>
  // Statistic of a histogram metric
  stat?: HistogramStat
  // One series of all points
  merge?: boolean
  // IANA zone the buckets follow (browserTimeZone in lib/api)
  timeZone?: string
}

export interface BatchSeriesParams {
  queries: MetricQuery[]
  intervalSeconds?: number
  timeZone?: string
}

export interface MetricAttributeKeysParams {
  name: string
  service?: string
}

export interface BreakdownValuesParams {
  name: string
  attribute: string
  service?: string
}

// Empty strings are not filters: leave them out of keys and requests, so ''
// and undefined share a cache entry
function clean<T extends object>(params: T): T {
  const result = {} as Record<string, unknown>
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '' && value !== false) {
      result[key] = value
    }
  }
  return result as T
}

// The key part of an optional range: null covers all stored data
function rangeKey(range: RangeSource | undefined): TimeSelectionKey | null {
  return range?.key ?? null
}

function rangeParams(range: RangeSource | undefined): { from?: string; to?: string } {
  return range ? resolveRangeParams(range) : {}
}

export const queryKeys = {
  stats: {
    all: ['stats'] as const,
    summary: (range?: RangeSource) => ['stats', rangeKey(range)] as const,
  },
  providers: ['providers'] as const,
  status: ['status'] as const,
  services: ['services'] as const,
  traces: {
    all: ['traces'] as const,
    lists: ['traces', 'list'] as const,
    list: (filters: TraceFilters, range: RangeSource, page: PageParams) =>
      ['traces', 'list', clean(filters), range.key, page] as const,
    recent: (limit: number, range?: RangeSource) => ['traces', 'recent', limit, rangeKey(range)] as const,
    spans: (id: string, kind: TraceKind) => ['traces', 'spans', kind, id] as const,
  },
  metrics: {
    all: ['metrics'] as const,
    names: (service?: string) => ['metrics', 'names', service || null] as const,
    series: (params: MetricSeriesParams, range: RangeSource) => ['metrics', 'series', clean(params), range.key] as const,
    batchSeries: (params: BatchSeriesParams, range: RangeSource) =>
      ['metrics', 'batch-series', params.queries, params.intervalSeconds ?? null, params.timeZone ?? null, range.key] as const,
    attributeKeys: (params: MetricAttributeKeysParams) => ['metrics', 'attribute-keys', clean(params)] as const,
    breakdownValues: (params: BreakdownValuesParams) => ['metrics', 'breakdown-values', clean(params)] as const,
    points: (filters: MetricPointFilters, range: RangeSource, page: PageParams) =>
      ['metrics', 'points', clean(filters), range.key, page] as const,
  },
  logs: {
    all: ['logs'] as const,
    lists: ['logs', 'list'] as const,
    list: (filters: LogFilters, range: RangeSource, page: PageParams) => ['logs', 'list', clean(filters), range.key, page] as const,
    levels: (range?: RangeSource) => ['logs', 'levels', rangeKey(range)] as const,
  },
  sessions: {
    all: ['sessions'] as const,
    lists: ['sessions', 'list'] as const,
    list: (filters: SessionFilters, range: RangeSource, page: PageParams) =>
      ['sessions', 'list', clean(filters), range.key, page] as const,
    transcript: (sessionId: string) => ['sessions', 'transcript', sessionId] as const,
    // Not under transcript(id), so invalidating a transcript's pages leaves it
    transcriptSearch: (sessionId: string, search: string) => ['sessions', 'transcript-search', sessionId, search] as const,
  },
  // For the dashboard store's mutations: invalidate dashboards.all after a change
  dashboards: {
    all: ['dashboards'] as const,
    list: ['dashboards', 'list'] as const,
    default: ['dashboards', 'default'] as const,
    detail: (id: string) => ['dashboards', 'detail', id] as const,
  },
  // The HTTP Inspector. Lists and summaries live under runs, run(id) and
  // session(id); an exchange's detail, bodies and events under exchange(id),
  // which live notifications only refresh while the exchange is in progress.
  httpInspector: {
    all: ['http-inspector'] as const,
    status: ['http-inspector', 'status'] as const,
    runs: ['http-inspector', 'runs'] as const,
    runList: (filters: HTTPRunFilters) => ['http-inspector', 'runs', clean(filters)] as const,
    run: (runId: string) => ['http-inspector', 'run', runId] as const,
    runDetail: (runId: string) => ['http-inspector', 'run', runId, 'detail'] as const,
    runExchanges: (runId: string, filters: HTTPExchangeFilters) => ['http-inspector', 'run', runId, 'exchanges', clean(filters)] as const,
    runTools: (runId: string) => ['http-inspector', 'run', runId, 'tools'] as const,
    sessions: ['http-inspector', 'session'] as const,
    inspectorSessions: ['http-inspector', 'sessions'] as const,
    // Below inspectorSessions, so what refreshes the list refreshes it too
    capturesStored: ['http-inspector', 'sessions', 'any'] as const,
    sessionTurns: (sessionId: string) => ['http-inspector', 'session', sessionId, 'turns'] as const,
    session: (sessionId: string) => ['http-inspector', 'session', sessionId] as const,
    sessionSummary: (sessionId: string) => ['http-inspector', 'session', sessionId, 'summary'] as const,
    sessionExchanges: (sessionId: string, filters: HTTPExchangeFilters) =>
      ['http-inspector', 'session', sessionId, 'exchanges', clean(filters)] as const,
    exchange: (exchangeId: string) => ['http-inspector', 'exchange', exchangeId] as const,
    exchangeDetail: (exchangeId: string) => ['http-inspector', 'exchange', exchangeId, 'detail'] as const,
    exchangeBody: (exchangeId: string, part: HTTPBodyPart, variant: 'safe' | 'raw') =>
      ['http-inspector', 'exchange', exchangeId, 'body', part, variant] as const,
    exchangeEvents: (exchangeId: string, type: string) => ['http-inspector', 'exchange', exchangeId, 'events', type || null] as const,
    exchangeEventTypes: (exchangeId: string) => ['http-inspector', 'exchange', exchangeId, 'event-types'] as const,
    exchangeUsage: (exchangeId: string) => ['http-inspector', 'exchange', exchangeId, 'usage'] as const,
    exchangeDiff: (exchangeId: string) => ['http-inspector', 'exchange', exchangeId, 'diff'] as const,
  },
  // useNewItems: count of a list's items since an anchor (Unix ms), asked
  // again when more records were announced in real time
  newItems: (list: string, filters: object, since: number, announced: number) =>
    ['new-items', list, clean(filters), since, announced] as const,
}

// Lists keep showing the previous page while the next one (or the list for
// changed filters) loads; isPlaceholderData tells pages to dim it
const listOptions = { placeholderData: keepPreviousData }

// Lists of what the backend knows change rarely: one fetch serves every page
const SERVICES_STALE_MS = 5 * 60_000

interface QueryOptions {
  enabled?: boolean
}

// ============================================================================
// Stats, providers, services
// ============================================================================

/** Aggregate statistics; without a range all stored telemetry */
export function useStats(range?: RangeSource, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.stats.summary(range),
    queryFn: ({ signal }) => api.getStats(rangeParams(range), { signal }),
    placeholderData: keepPreviousData,
    ...options,
  })
}

/**
 * The supported tools (GET /api/providers). Loaded once per app; the
 * result also feeds providerStore, which getServiceDisplayName and
 * serviceFilterOptions read (React components use useServiceNames to
 * re-render when it arrives). Mounted by the layout.
 */
export function useProviders() {
  const query = useQuery({
    queryKey: queryKeys.providers,
    queryFn: ({ signal }) => api.getProviders({ signal }),
    staleTime: Infinity,
  })
  const setProviders = useProviderStore((s) => s.setProviders)
  useEffect(() => {
    if (query.data) {
      setProviders(query.data.providers ?? [])
    }
  }, [query.data, setProviders])
  return query
}

/** Services that have sent telemetry; shared by every page's service filter */
export function useServices(options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.services,
    queryFn: ({ signal }) => api.getServices({ signal }),
    select: (data) => data.services ?? [],
    staleTime: SERVICES_STALE_MS,
    ...options,
  })
}

// ============================================================================
// Traces
// ============================================================================

export function useTraces(filters: TraceFilters, range: RangeSource, page: PageParams, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.traces.list(filters, range, page),
    queryFn: ({ signal }) =>
      api.getTraces({ ...clean(filters), ...resolveRangeParams(range), limit: page.limit, cursor: page.cursor }, { signal }),
    ...listOptions,
    ...options,
  })
}

export function useRecentTraces(limit: number, range?: RangeSource, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.traces.recent(limit, range),
    queryFn: ({ signal }) => api.getRecentTraces(limit, rangeParams(range), { signal }),
    ...listOptions,
    ...options,
  })
}

/**
 * The spans of a trace view, a page (1,000 spans) at a time: data.pages[i]
 * is a SpansResponse; fetchNextPage() loads the next while hasNextPage.
 */
export function useTraceSpans(id: string, kind: TraceKind, options: QueryOptions = {}) {
  return useInfiniteQuery({
    queryKey: queryKeys.traces.spans(id, kind),
    queryFn: ({ signal, pageParam }) => api.getTraceSpans(id, kind, { signal }, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.hasMore && last.nextCursor ? last.nextCursor : undefined),
    ...options,
  })
}

// ============================================================================
// Metrics
// ============================================================================

export function useMetricNames(service?: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.metrics.names(service),
    queryFn: ({ signal }) => api.getMetricNames(service || undefined, { signal }),
    select: (data) => data.names ?? [],
    staleTime: SERVICES_STALE_MS,
    ...options,
  })
}

export function useMetricSeries(params: MetricSeriesParams, range: RangeSource, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.metrics.series(params, range),
    queryFn: ({ signal }) => api.getMetricSeries({ ...clean(params), name: params.name, ...resolveRangeParams(range) }, { signal }),
    placeholderData: keepPreviousData,
    ...options,
    enabled: Boolean(params.name) && (options.enabled ?? true),
  })
}

/** Several series in one request (dashboard widgets); data.results by query id */
export function useBatchMetricSeries(params: BatchSeriesParams, range: RangeSource, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.metrics.batchSeries(params, range),
    queryFn: ({ signal }) =>
      api.getBatchMetricSeries(
        { ...resolveRangeParams(range), intervalSeconds: params.intervalSeconds, timeZone: params.timeZone, queries: params.queries },
        { signal },
      ),
    placeholderData: keepPreviousData,
    ...options,
    enabled: params.queries.length > 0 && (options.enabled ?? true),
  })
}

export function useBreakdownValues(params: BreakdownValuesParams, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.metrics.breakdownValues(params),
    queryFn: ({ signal }) => api.getBreakdownValues({ ...clean(params), name: params.name, attribute: params.attribute }, { signal }),
    select: (data) => data.values ?? [],
    ...options,
    enabled: Boolean(params.name && params.attribute) && (options.enabled ?? true),
  })
}

/** The attribute keys of a metric's points (what series can be grouped by) */
export function useMetricAttributeKeys(params: MetricAttributeKeysParams, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.metrics.attributeKeys(params),
    queryFn: ({ signal }) => api.getMetricAttributeKeys({ ...clean(params), name: params.name }, { signal }),
    select: (data) => data.keys ?? [],
    staleTime: SERVICES_STALE_MS,
    ...options,
    enabled: Boolean(params.name) && (options.enabled ?? true),
  })
}

/** Raw metric data points (GET /api/metrics) */
export function useMetricPoints(filters: MetricPointFilters, range: RangeSource, page: PageParams, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.metrics.points(filters, range, page),
    queryFn: ({ signal }) =>
      api.getMetrics({ ...clean(filters), ...resolveRangeParams(range), limit: page.limit, cursor: page.cursor }, { signal }),
    ...listOptions,
    ...options,
  })
}

// ============================================================================
// Logs
// ============================================================================

export function useLogs(filters: LogFilters, range: RangeSource, page: PageParams, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.logs.list(filters, range, page),
    queryFn: ({ signal }) =>
      api.getLogs({ ...clean(filters), ...resolveRangeParams(range), limit: page.limit, cursor: page.cursor }, { signal }),
    ...listOptions,
    ...options,
  })
}

/** Log counts by severity; without a range all logs */
export function useLogLevels(range?: RangeSource, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.logs.levels(range),
    queryFn: ({ signal }) => api.getLogLevels(rangeParams(range), { signal }),
    placeholderData: keepPreviousData,
    ...options,
  })
}

// ============================================================================
// Sessions
// ============================================================================

export function useSessions(filters: SessionFilters, range: RangeSource, page: PageParams, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.sessions.list(filters, range, page),
    queryFn: ({ signal }) =>
      api.getSessions({ ...clean(filters), ...resolveRangeParams(range), limit: page.limit, cursor: page.cursor }, { signal }),
    ...listOptions,
    ...options,
  })
}

/**
 * A session's transcript, a page at a time: data.pages[i] is a
 * TranscriptResponse (oldest messages first); fetchNextPage() loads the
 * next while hasNextPage.
 */
export function useSessionTranscript(sessionId: string, options: QueryOptions = {}) {
  return useInfiniteQuery({
    queryKey: queryKeys.sessions.transcript(sessionId),
    queryFn: ({ signal, pageParam }) => api.getSessionTranscript(sessionId, { signal }, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.hasMore && last.nextCursor ? last.nextCursor : undefined),
    ...options,
    enabled: Boolean(sessionId) && (options.enabled ?? true),
  })
}

/**
 * The positions of the messages of a whole transcript that contain all
 * terms of search (data: { terms, matches }); disabled for an empty search.
 * Asks for one message only: the matches cover the whole transcript.
 */
export function useTranscriptSearch(sessionId: string, search: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.sessions.transcriptSearch(sessionId, search),
    queryFn: async ({ signal }) => {
      const page = await api.getSessionTranscript(sessionId, { signal }, undefined, { search, limit: 1 })
      return page.search ?? { terms: [], matches: [] }
    },
    ...options,
    enabled: Boolean(sessionId && search.trim()) && (options.enabled ?? true),
  })
}

// ============================================================================
// HTTP Inspector
// ============================================================================

// Pages of exchange lists and stream events: the API's largest pages, since
// the lists are virtualized
export const HTTP_EXCHANGES_PAGE_SIZE = 500
export const HTTP_EVENTS_PAGE_SIZE = 1000
export const HTTP_RUNS_PAGE_SIZE = 50
// Sessions per page of the HTTP Inspector's session list
export const HTTP_SESSIONS_PAGE_SIZE = 25

function nextCursor(last: { hasMore: boolean; nextCursor?: string }): string | undefined {
  return last.hasMore && last.nextCursor ? last.nextCursor : undefined
}

/** The capture proxy: configuration and counters (GET /api/http-inspector/status) */
export function useHTTPInspectorStatus(options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.status,
    queryFn: ({ signal }) => api.getHTTPInspectorStatus({ signal }),
    ...options,
  })
}

/** Capture runs, newest first, a page at a time (data.pages[i].runs) */
export function useHTTPCaptureRuns(filters: HTTPRunFilters = {}, options: QueryOptions = {}) {
  return useInfiniteQuery({
    queryKey: queryKeys.httpInspector.runList(filters),
    queryFn: ({ signal, pageParam }) =>
      api.getHTTPCaptureRuns({ ...clean(filters), limit: HTTP_RUNS_PAGE_SIZE, cursor: pageParam }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
    ...options,
  })
}

/** Sessions with captured traffic, a page at a time (data.pages[i].sessions) */
export function useHTTPInspectorSessions(options: QueryOptions = {}) {
  return useInfiniteQuery({
    queryKey: queryKeys.httpInspector.inspectorSessions,
    queryFn: ({ signal, pageParam }) => api.getHTTPInspectorSessions({ limit: HTTP_SESSIONS_PAGE_SIZE, cursor: pageParam }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
    ...options,
  })
}

/** Whether any captured exchange is stored (a one-row page of the session list) */
export function useHTTPCapturesStored(options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.capturesStored,
    queryFn: ({ signal }) => api.getHTTPInspectorSessions({ limit: 1 }, { signal }),
    select: (data) => data.total > 0 || data.sessions.length > 0,
    staleTime: 60_000,
    ...options,
  })
}

/** A session's captured traffic grouped into turns ('' for the exchanges without a session) */
export function useHTTPSessionTurns(sessionId: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.sessionTurns(sessionId),
    queryFn: ({ signal }) => api.getHTTPSessionTurns(sessionId, { signal }),
    ...options,
  })
}

/** A capture run with its summary */
export function useHTTPCaptureRun(runId: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.runDetail(runId),
    queryFn: ({ signal }) => api.getHTTPCaptureRun(runId, { signal }),
    ...options,
    enabled: Boolean(runId) && (options.enabled ?? true),
  })
}

/** A run's exchanges in arrival order, 500 a page (data.pages[i].exchanges) */
export function useHTTPRunExchanges(runId: string, filters: HTTPExchangeFilters, options: QueryOptions = {}) {
  return useInfiniteQuery({
    queryKey: queryKeys.httpInspector.runExchanges(runId, filters),
    queryFn: ({ signal, pageParam }) =>
      api.getHTTPRunExchanges(runId, { ...clean(filters), limit: HTTP_EXCHANGES_PAGE_SIZE, cursor: pageParam }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
    placeholderData: keepPreviousData,
    ...options,
    enabled: Boolean(runId) && (options.enabled ?? true),
  })
}

/** A run's tool uses and the exchanges that requested and returned them */
export function useHTTPRunTools(runId: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.runTools(runId),
    queryFn: ({ signal }) => api.getHTTPRunTools(runId, { signal }),
    ...options,
    enabled: Boolean(runId) && (options.enabled ?? true),
  })
}

/**
 * The tool links of a session: those of the runs that carried it, kept
 * when their session is this one (tool links are listed per run). note is
 * the server's reminder that local execution is not observed.
 */
export function useSessionToolLinks(sessionId: string, runIds: string[], options: QueryOptions = {}) {
  return useQueries({
    queries: runIds.map((runId) => ({
      queryKey: queryKeys.httpInspector.runTools(runId),
      queryFn: ({ signal }: { signal: AbortSignal }) => api.getHTTPRunTools(runId, { signal }),
      enabled: options.enabled ?? true,
    })),
    combine: (results) => {
      const tools: HTTPToolLink[] = []
      let note = ''
      for (const result of results) {
        note ||= result.data?.note ?? ''
        for (const link of result.data?.tools ?? []) {
          if (link.sessionId === sessionId) tools.push(link)
        }
      }
      const failed = results.find((r) => r.isError)
      return {
        tools,
        note,
        isPending: results.some((r) => r.isPending),
        isError: Boolean(failed),
        error: failed?.error ?? null,
        isFetching: results.some((r) => r.isFetching),
        refetch: () => results.forEach((r) => void r.refetch()),
      }
    },
  })
}

/**
 * The summary of a session's API traffic (all its exchanges, whatever the
 * filters), the runs that carried it and the comparison with its telemetry.
 * An empty summary (exchanges 0) when nothing was captured.
 */
export function useSessionTrafficSummary(sessionId: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.sessionSummary(sessionId),
    queryFn: ({ signal }) => api.getSessionHTTPExchanges(sessionId, { limit: 1 }, { signal }),
    // Shared by the page's tab badge and the tab itself: opening the tab
    // does not load it again (new exchanges invalidate it)
    staleTime: 10_000,
    ...options,
    enabled: Boolean(sessionId) && (options.enabled ?? true),
  })
}

/** A session's exchanges in start order, 500 a page */
export function useSessionExchanges(sessionId: string, filters: HTTPExchangeFilters, options: QueryOptions = {}) {
  return useInfiniteQuery({
    queryKey: queryKeys.httpInspector.sessionExchanges(sessionId, filters),
    queryFn: ({ signal, pageParam }) =>
      api.getSessionHTTPExchanges(sessionId, { ...clean(filters), limit: HTTP_EXCHANGES_PAGE_SIZE, cursor: pageParam }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
    placeholderData: keepPreviousData,
    ...options,
    enabled: Boolean(sessionId) && (options.enabled ?? true),
  })
}

/** An exchange with its lifecycle, observations and run neighbours */
export function useHTTPExchange(exchangeId: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.exchangeDetail(exchangeId),
    queryFn: ({ signal }) => api.getHTTPExchange(exchangeId, { signal }),
    ...options,
    enabled: Boolean(exchangeId) && (options.enabled ?? true),
  })
}

/** One side of an exchange (headers, body, interpretation) */
export function useHTTPExchangeBody(exchangeId: string, part: HTTPBodyPart, variant: 'safe' | 'raw' = 'safe', options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.exchangeBody(exchangeId, part, variant),
    queryFn: ({ signal }) => api.getHTTPExchangeBody(exchangeId, part, variant, { signal }),
    ...options,
    enabled: Boolean(exchangeId) && (options.enabled ?? true),
  })
}

/** A streamed response's events in order, 1,000 a page; type filters them */
export function useHTTPExchangeEvents(exchangeId: string, type: string, options: QueryOptions = {}) {
  return useInfiniteQuery({
    queryKey: queryKeys.httpInspector.exchangeEvents(exchangeId, type),
    queryFn: ({ signal, pageParam }) =>
      api.getHTTPExchangeEvents(exchangeId, { type: type || undefined, limit: HTTP_EVENTS_PAGE_SIZE, cursor: pageParam }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: nextCursor,
    placeholderData: keepPreviousData,
    ...options,
    enabled: Boolean(exchangeId) && (options.enabled ?? true),
  })
}

/** The counts of a streamed response's events by type (one event read) */
export function useHTTPExchangeEventTypes(exchangeId: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.exchangeEventTypes(exchangeId),
    queryFn: ({ signal }) => api.getHTTPExchangeEvents(exchangeId, { limit: 1 }, { signal }),
    select: (data) => data.types ?? {},
    ...options,
    enabled: Boolean(exchangeId) && (options.enabled ?? true),
  })
}

/** An exchange's usage, stream snapshots and cost; null without usage */
export function useHTTPExchangeUsage(exchangeId: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.exchangeUsage(exchangeId),
    queryFn: ({ signal }) => api.getHTTPExchangeUsage(exchangeId, { signal }),
    ...options,
    enabled: Boolean(exchangeId) && (options.enabled ?? true),
  })
}

/** The request compared with the previous one of its conversation */
export function useHTTPExchangeDiff(exchangeId: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.httpInspector.exchangeDiff(exchangeId),
    queryFn: ({ signal }) => api.getHTTPExchangeDiff(exchangeId, { signal }),
    ...options,
    enabled: Boolean(exchangeId) && (options.enabled ?? true),
  })
}

// ============================================================================
// Server status
// ============================================================================

/** GET /api/status (the status page and the sidebar's version) */
export function useStatus(options: QueryOptions & Pick<UseQueryOptions<StatusResponse>, 'refetchInterval'> = {}) {
  return useQuery({
    queryKey: queryKeys.status,
    queryFn: ({ signal }) => api.getStatus({ signal }),
    staleTime: 30_000,
    ...options,
  })
}

// ============================================================================
// Dashboards
// ============================================================================

// The server creates the default dashboard when it is first asked for it:
// a list requested before that lacks it
function lacksDashboard(list: DashboardsResponse | undefined, dashboard: DashboardWithWidgets | null | undefined): boolean {
  return Boolean(dashboard) && !list?.dashboards?.some((d) => d.id === dashboard?.id)
}

export function useDashboards(options: QueryOptions = {}) {
  const queryClient = useQueryClient()
  return useQuery({
    queryKey: queryKeys.dashboards.list,
    queryFn: async ({ signal }) => {
      const list = await api.getDashboards({ signal })
      const known = queryClient.getQueryData<DashboardWithWidgets | null>(queryKeys.dashboards.default)
      const creating = queryClient.getQueryState(queryKeys.dashboards.default)?.fetchStatus === 'fetching'
      if ((list.dashboards ?? []).length === 0 && known === undefined && creating) {
        // A new database while the dashboard page asks for the default
        // dashboard: wait for that request, then list again. Never asked
        // here otherwise, so a deleted last dashboard is not recreated.
        try {
          const created = await queryClient.fetchQuery({
            queryKey: queryKeys.dashboards.default,
            queryFn: ({ signal }) => api.getDefaultDashboard({ signal }),
          })
          return created ? await api.getDashboards({ signal }) : list
        } catch {
          return list
        }
      }
      // Sent before the default dashboard request created it: ask once more
      return lacksDashboard(list, known) ? api.getDashboards({ signal }) : list
    },
    select: (data) => data.dashboards ?? [],
    ...options,
  })
}

/** The default dashboard with its widgets; the server creates it when none exists (null only from servers before that) */
export function useDefaultDashboard(options: QueryOptions = {}) {
  const queryClient = useQueryClient()
  return useQuery({
    queryKey: queryKeys.dashboards.default,
    queryFn: async ({ signal }) => {
      const dashboard = await api.getDefaultDashboard({ signal })
      // This request may have created it: a list loaded before lacks it (one
      // still loading checks for itself, see useDashboards)
      const list = queryClient.getQueryData<DashboardsResponse>(queryKeys.dashboards.list)
      if (list && lacksDashboard(list, dashboard)) {
        void queryClient.invalidateQueries({ queryKey: queryKeys.dashboards.list, exact: true })
      }
      return dashboard
    },
    ...options,
  })
}

export function useDashboard(id: string, options: QueryOptions = {}) {
  return useQuery({
    queryKey: queryKeys.dashboards.detail(id),
    queryFn: ({ signal }) => api.getDashboard(id, { signal }),
    ...options,
    enabled: Boolean(id) && (options.enabled ?? true),
  })
}

// ============================================================================
// Counts for "N new items" (useNewItems): the list endpoint with the list's
// filters and limit 1, reading its total
// ============================================================================

export type CountBetween = (range: { from: Date; to: Date }, signal: AbortSignal) => Promise<number>

export function countTraces(filters: TraceFilters): CountBetween {
  return async ({ from, to }, signal) =>
    (await api.getTraces({ ...clean(filters), from: from.toISOString(), to: to.toISOString(), limit: 1 }, { signal })).total ?? 0
}

export function countLogs(filters: LogFilters): CountBetween {
  return async ({ from, to }, signal) =>
    (await api.getLogs({ ...clean(filters), from: from.toISOString(), to: to.toISOString(), limit: 1 }, { signal })).total ?? 0
}

export function countSessions(filters: SessionFilters): CountBetween {
  return async ({ from, to }, signal) =>
    (await api.getSessions({ ...clean(filters), from: from.toISOString(), to: to.toISOString(), limit: 1 }, { signal })).total ?? 0
}
