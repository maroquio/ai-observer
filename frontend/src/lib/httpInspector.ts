// Labels, paths and formatting of the HTTP Inspector (the Anthropic API
// traffic the capture proxy observed). Pure code: the components in
// components/http-inspector render what these return.
//
// Captured payloads are untrusted: they are only ever shown as text (React
// escapes it), never as HTML, Markdown or a link target.

import { formatCount, formatDurationMs } from '@/lib/format'
import type {
  HTTPCaptureMode,
  HTTPCaptureRun,
  HTTPCaptureRunMode,
  HTTPCaptureRunState,
  HTTPCostStatus,
  HTTPExchange,
  HTTPExchangeKind,
  HTTPExchangeState,
  HTTPToolLinkStatus,
  HTTPUsageQuality,
  WireCertainty,
} from '@/types/httpInspector'

// ============================================================================
// Labels
// ============================================================================

export const KIND_LABELS: Record<HTTPExchangeKind, string> = {
  model_inference: 'Inferência',
  token_count: 'Contagem de tokens',
  quota_probe: 'Sonda de cota (auxiliar)',
  other_api: 'Outro endpoint',
  transport_error: 'Erro de transporte',
}

export const STATE_LABELS: Record<HTTPExchangeState, string> = {
  in_progress: 'Em andamento',
  complete: 'Completa',
  partial: 'Parcial',
  aborted: 'Abortada pelo cliente',
  failed: 'Falhou (sem resposta)',
  interrupted: 'Interrompida',
}

export const CAPTURE_MODE_LABELS: Record<HTTPCaptureMode, string> = {
  safe: 'Seguro (segredos mascarados)',
  metadata_only: 'Só metadados (sem corpos)',
  full_raw: 'Bruto completo (local e sensível)',
}

export const RUN_MODE_LABELS: Record<HTTPCaptureRunMode, string> = {
  manual: 'Manual (porta compartilhada)',
  launcher: 'Launcher (ai-observer run)',
}

export const RUN_STATE_LABELS: Record<HTTPCaptureRunState, string> = {
  active: 'Ativa',
  finished: 'Finalizada',
  interrupted: 'Interrompida',
}

export const USAGE_QUALITY_LABELS: Record<HTTPUsageQuality, string> = {
  final: 'Usage final',
  partial: 'Usage parcial',
  unavailable: 'Usage indisponível',
}

export const COST_STATUS_LABELS: Record<HTTPCostStatus, string> = {
  complete: 'Completo',
  partial: 'Parcial',
  unavailable: 'Indisponível',
}

export const TOOL_LINK_STATUS_LABELS: Record<HTTPToolLinkStatus, string> = {
  matched: 'Resultado encontrado',
  pending: 'Aguardando resultado',
  unresolved: 'Sem correspondência',
  ambiguous: 'Ambíguo',
}

export const CERTAINTY_LABELS: Record<WireCertainty, string> = {
  observed: 'Observado',
  inferred: 'Inferido',
}

const SIDE_LABELS: Record<string, string> = {
  request: 'Requisição',
  response: 'Resposta',
  transport: 'Transporte',
}

export function sideLabel(side: string): string {
  return SIDE_LABELS[side] ?? side
}

const LIFECYCLE_LABELS: Record<string, string> = {
  request_started: 'Requisição iniciada',
  request_body_observed: 'Corpo da requisição observado',
  upstream_connected: 'Conectado à API',
  response_headers_received: 'Cabeçalhos da resposta recebidos',
  response_streaming: 'Resposta em streaming',
  completed: 'Concluída',
  upstream_error: 'Erro ao falar com a API',
  client_aborted: 'Cliente abortou',
  upstream_aborted: 'API encerrou antes do fim',
  capture_degraded: 'Captura degradada',
  stream_parse_error: 'Erro ao interpretar o stream',
}

export function lifecycleLabel(event: string): string {
  return LIFECYCLE_LABELS[event] ?? event
}

const COST_COMPONENT_LABELS: Record<string, string> = {
  input: 'Entrada (sem cache)',
  output: 'Saída',
  cache_read: 'Leitura de cache',
  cache_write_5m: 'Escrita de cache (5 min)',
  cache_write_1h: 'Escrita de cache (1 h)',
  cache_write_unknown: 'Escrita de cache (duração não informada)',
}

export function costComponentLabel(component: string): string {
  return COST_COMPONENT_LABELS[component] ?? component
}

const COMPARED_BY_LABELS: Record<string, string> = {
  same_conversation: 'mesma conversa (primeira mensagem igual)',
  same_model: 'mesmo modelo',
  previous_in_session: 'requisição anterior da sessão',
  none: 'nenhuma',
}

export function comparedByLabel(comparedBy: string): string {
  return COMPARED_BY_LABELS[comparedBy] ?? comparedBy
}

const CERTAINTY_COMPARISON_LABELS: Record<string, string> = {
  exact: 'Exata',
  partial: 'Parcial',
  none: 'Sem correspondência',
}

export function comparisonCertaintyLabel(certainty: string): string {
  return CERTAINTY_COMPARISON_LABELS[certainty] ?? certainty
}

// ============================================================================
// Where data comes from
// ============================================================================

/**
 * The origin of a shown value: wire (observed by the proxy), otel and jsonl
 * (the session's telemetry, compared but never added), derivado (computed
 * from observed values: totals, costs, inferred statements)
 */
export type DataOrigin = 'wire' | 'otel' | 'jsonl' | 'derivado'

export const ORIGIN_LABELS: Record<DataOrigin, string> = {
  wire: 'wire',
  otel: 'otel',
  jsonl: 'jsonl',
  derivado: 'derivado',
}

export const ORIGIN_DESCRIPTIONS: Record<DataOrigin, string> = {
  wire: 'Observado na rede pelo proxy',
  otel: 'Telemetria OTLP da ferramenta',
  jsonl: 'Arquivo de sessão local (JSONL)',
  derivado: 'Calculado ou inferido a partir do que foi observado',
}

/** Observations stated from the wire are wire; conclusions drawn from it are derived */
export function observationOrigin(certainty: WireCertainty): DataOrigin {
  return certainty === 'observed' ? 'wire' : 'derivado'
}

// ============================================================================
// Session evidence (how an exchange was linked to its session)
// ============================================================================

export type LinkQuality = 'header' | 'metadata' | 'launcher' | 'none'

export const LINK_QUALITY_LABELS: Record<LinkQuality, string> = {
  header: 'Cabeçalho X-Claude-Code-Session-Id',
  metadata: 'metadata.user_id do corpo',
  launcher: 'Somente a execução do launcher',
  none: 'Não correlacionado',
}

export const LINK_QUALITY_DESCRIPTIONS: Record<LinkQuality, string> = {
  header: 'O Claude Code enviou o ID da sessão no cabeçalho da requisição (vínculo direto).',
  metadata: 'O ID da sessão veio do campo metadata.user_id do corpo da requisição.',
  launcher: 'Sem ID de sessão; a troca pertence a uma execução iniciada por ai-observer run.',
  none: 'Nenhuma evidência liga esta troca a uma sessão.',
}

/** How an exchange was linked to a session; runMode is its run's mode, if known */
export function linkQuality(exchange: Pick<HTTPExchange, 'sessionId' | 'sessionEvidence'>, runMode?: HTTPCaptureRunMode): LinkQuality {
  if (exchange.sessionId) {
    const evidence = exchange.sessionEvidence ?? ''
    if (evidence.startsWith('header:')) return 'header'
    if (evidence.includes('metadata.user_id')) return 'metadata'
    // A session ID without known evidence counts as the weaker one
    return 'metadata'
  }
  return runMode === 'launcher' ? 'launcher' : 'none'
}

/** Counts of exchanges by link quality */
export function linkQualityCounts(exchanges: HTTPExchange[], runs: Pick<HTTPCaptureRun, 'id' | 'mode'>[] = []): Record<LinkQuality, number> {
  const modes = new Map(runs.map((r) => [r.id, r.mode]))
  const counts: Record<LinkQuality, number> = { header: 0, metadata: 0, launcher: 0, none: 0 }
  for (const exchange of exchanges) {
    counts[linkQuality(exchange, modes.get(exchange.runId))]++
  }
  return counts
}

// ============================================================================
// Paths
// ============================================================================

/** Where an exchange is shown: under its session's page, or under its run */
export interface ExchangeContext {
  sessionId?: string
  runId?: string
}

export const HTTP_INSPECTOR_PATH = '/http-inspector'

/** The page of a capture run (the CLI prints this path) */
export function runPath(runId: string): string {
  return `${HTTP_INSPECTOR_PATH}/runs/${encodeURIComponent(runId)}`
}

/** The session page with its API traffic tab */
export function sessionTrafficPath(sessionId: string): string {
  return `/sessions/${encodeURIComponent(sessionId)}?view=traffic`
}

// The URL parameter of the exchanges without a session
export const NO_SESSION_PARAM = 'none'

/** The HTTP Inspector's page of a session ('' for the exchanges without one) */
export function inspectorSessionPath(sessionId: string): string {
  return `${HTTP_INSPECTOR_PATH}/sessions/${encodeURIComponent(sessionId || NO_SESSION_PARAM)}`
}

/** The session ID of an inspector session page's URL parameter */
export function sessionIdFromParam(param: string): string {
  return param === NO_SESSION_PARAM ? '' : param
}

/** A short form of a session ID for titles (its first 8 characters) */
export function shortSessionId(sessionId: string): string {
  return sessionId.length > 12 ? `${sessionId.slice(0, 8)}…` : sessionId
}

/** Exchanges that are not model inferences: count_tokens, quota probes, other endpoints, transport errors */
export function isAuxiliaryExchange(exchange: Pick<HTTPExchange, 'kind'>): boolean {
  return exchange.kind !== 'model_inference'
}

/** The requested tools with how often each was requested, in order of first use */
export function toolUseCounts(names: string[]): Array<{ name: string; count: number }> {
  const counts = new Map<string, number>()
  for (const name of names) counts.set(name, (counts.get(name) ?? 0) + 1)
  return [...counts].map(([name, count]) => ({ name, count }))
}

/** The inspector of an exchange, in the session's or the run's context */
export function exchangePath(context: ExchangeContext, exchangeId: string): string {
  const id = encodeURIComponent(exchangeId)
  if (context.sessionId) {
    return `/sessions/${encodeURIComponent(context.sessionId)}/http-exchanges/${id}`
  }
  return `${runPath(context.runId ?? '')}/exchanges/${id}`
}

// ============================================================================
// Formatting
// ============================================================================

/** A count that may be absent (null/undefined: not reported, never 0) */
export function formatMaybeCount(n: number | null | undefined): string | null {
  return n === undefined || n === null ? null : formatCount(n)
}

/** A duration in ms that may not have been reached */
export function formatMaybeMs(ms: number | null | undefined): string | null {
  return ms === undefined || ms === null ? null : formatDurationMs(ms)
}

/** A share (0..1) as a percentage: 0.4567 → "45.7%" */
export function formatShare(share: number | null | undefined): string | null {
  return share === undefined || share === null || !Number.isFinite(share) ? null : `${(share * 100).toFixed(1)}%`
}

/** A time of day with milliseconds, in the browser's zone: "14:03:07.120" */
export function formatClock(timestamp: string): string {
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return timestamp
  const pad = (n: number, width = 2) => String(n).padStart(width, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`
}

/** Milliseconds from start to at (lifecycle events): "+120ms", "+2.5s" */
export function formatOffset(start: string, at: string): string | null {
  const ms = new Date(at).getTime() - new Date(start).getTime()
  return Number.isFinite(ms) ? `+${formatDurationMs(Math.max(0, ms))}` : null
}

/** method and path of an exchange, with its query */
export function endpointLabel(exchange: Pick<HTTPExchange, 'method' | 'path' | 'query'>): string {
  return `${exchange.method} ${exchange.path}${exchange.query ? `?${exchange.query}` : ''}`
}

/** The model of an exchange: the reply's, else the requested one */
export function exchangeModel(exchange: Pick<HTTPExchange, 'responseModel' | 'requestedModel'>): string | undefined {
  return exchange.responseModel || exchange.requestedModel || undefined
}

/** Processed tokens of an exchange, or the count_tokens estimate */
export function exchangeTokens(exchange: HTTPExchange): { value: number; estimate: boolean } | null {
  const usage = exchange.usage
  if (!usage) return null
  if (usage.countTokens != null) {
    return { value: usage.countTokens, estimate: true }
  }
  const total = usage.totals?.totalProcessed
  return total == null ? null : { value: total, estimate: false }
}

/** Whether an exchange is an error: HTTP status ≥ 400, or no response (Claude Code's quota probe is auxiliary, never an error) */
export function isErrorExchange(exchange: Pick<HTTPExchange, 'status' | 'kind' | 'state'>): boolean {
  if (exchange.kind === 'quota_probe') return false
  return exchange.status >= 400 || exchange.status === 0 || exchange.kind === 'transport_error' || exchange.state === 'failed'
}

/** Kinds that carry a model's usage */
export function expectsUsage(kind: HTTPExchangeKind): boolean {
  return kind === 'model_inference'
}

// ============================================================================
// Payload text
// ============================================================================

/** JSON of a value, indented; '' for undefined */
export function jsonText(value: unknown): string {
  if (value === undefined) return ''
  try {
    return JSON.stringify(value, null, 2) ?? ''
  } catch {
    return String(value)
  }
}

/** A JSON document indented for reading; other text as it is */
export function prettyJSON(text: string): { text: string; json: boolean } {
  const trimmed = text.trim()
  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) {
    return { text, json: false }
  }
  try {
    return { text: JSON.stringify(JSON.parse(trimmed), null, 2), json: true }
  } catch {
    return { text, json: false }
  }
}

/** Parses a JSON document; undefined when it is none */
export function parseJSON(text: string | undefined): unknown {
  if (!text) return undefined
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

// Text beyond this many characters is shown on request: bodies reach 16 MB
export const MAX_RENDERED_CHARS = 100_000

/** The part of text that is rendered at first */
export function clipText(text: string, max = MAX_RENDERED_CHARS): { text: string; clipped: boolean } {
  return text.length > max ? { text: text.slice(0, max), clipped: true } : { text, clipped: false }
}

const REDACTION_MARK = /\[REDACTED:[^\]\s]+\]/

/** Whether text contains a redaction marker ([REDACTED:kind]) */
export function hasRedaction(text: string | undefined): boolean {
  return Boolean(text && REDACTION_MARK.test(text))
}

// ============================================================================
// Exchange list filters (kept in the URL)
// ============================================================================

/** URL parameters of the exchange filters and their defaults (all) */
export const EXCHANGE_FILTER_DEFAULTS = { kind: '', status: '', state: '', search: '' }
export type ExchangeFilterValues = typeof EXCHANGE_FILTER_DEFAULTS

function isKey<T extends string>(labels: Record<T, string>, value: string): value is T {
  return Object.prototype.hasOwnProperty.call(labels, value)
}

/**
 * The API filters of URL filter values; values the API would reject (from
 * an edited URL) are left out. search is passed separately (debounced).
 */
export function exchangeFiltersFromValues(
  values: ExchangeFilterValues,
  search: string,
): { kind?: HTTPExchangeKind; state?: HTTPExchangeState; status?: string; search?: string } {
  const status = values.status === 'ok' || values.status === 'error' || /^[1-5]\d\d$/.test(values.status) ? values.status : undefined
  return {
    kind: isKey(KIND_LABELS, values.kind) ? values.kind : undefined,
    state: isKey(STATE_LABELS, values.state) ? values.state : undefined,
    status,
    search: search.trim() || undefined,
  }
}

/** Whether any exchange filter is set */
export function hasExchangeFilters(values: ExchangeFilterValues): boolean {
  return Boolean(values.kind || values.status || values.state || values.search.trim())
}

/** Whether a parsed JSON value is an object (not an array or null) */
export function isJsonObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** The label of the Back button of HTTP Inspector pages, by where it leads */
export function backLabel(to: string): string {
  const path = to.split(/[?#]/, 1)[0]
  if (/^\/sessions\/[^/]+$/.test(path)) return 'Voltar à sessão'
  if (path === '/sessions') return 'Voltar às sessões'
  if (/^\/http-inspector\/runs\/[^/]+$/.test(path)) return 'Voltar à execução'
  if (path === HTTP_INSPECTOR_PATH) return 'Voltar ao HTTP Inspector'
  return 'Voltar'
}
