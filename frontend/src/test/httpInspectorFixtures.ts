import type {
  HTTPCaptureRun,
  HTTPExchange,
  HTTPExchangeBody,
  HTTPExchangeDetail,
  HTTPRequestDiffResponse,
  HTTPSSEEvent,
  HTTPSSEEventsResponse,
  HTTPSessionTrafficResponse,
  HTTPToolLinksResponse,
  HTTPTrafficSummary,
  HTTPUsage,
  HTTPUsageDetail,
  WireRequestDiff,
  WireRequestSummary,
} from '@/types/httpInspector'

// Fictional HTTP Inspector data for tests. Nothing here was captured from
// a real API; credential-shaped values are built at runtime.

export const SESSION_ID = '0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d'
export const RUN_ID = 'run-1'

export function runFixture(overrides: Partial<HTTPCaptureRun> = {}): HTTPCaptureRun {
  return {
    id: RUN_ID,
    name: 'Aula 3: refatoração',
    mode: 'launcher',
    state: 'finished',
    captureMode: 'safe',
    port: 41_000,
    upstream: 'https://api.anthropic.com',
    command: 'claude',
    cwd: '/home/aluno/projeto',
    pid: 4242,
    exitCode: 0,
    startedAt: '2026-11-20T14:00:00Z',
    endedAt: '2026-11-20T14:05:00Z',
    exchanges: 6,
    lastExchangeAt: '2026-11-20T14:04:00Z',
    sessionIds: [SESSION_ID],
    unattributed: 1,
    storedBytes: 123_456,
    ...overrides,
  }
}

export function usageFixture(overrides: Partial<HTTPUsage> = {}): HTTPUsage {
  return {
    usage: {
      inputTokens: 12,
      outputTokens: 340,
      cacheReadInputTokens: 18_000,
      cacheCreationInputTokens: 2_000,
      cacheCreation5m: 1_500,
      cacheCreation1h: 500,
    },
    totals: { totalInput: 20_012, totalProcessed: 20_352, cacheReadShare: 0.8995, complete: true },
    quality: 'final',
    costUsd: 0.0123,
    costStatus: 'complete',
    pricedModel: 'claude-sonnet-4-5',
    source: 'http_proxy',
    ...overrides,
  }
}

export function exchangeFixture(overrides: Partial<HTTPExchange> = {}): HTTPExchange {
  return {
    id: 'ex-1',
    runId: RUN_ID,
    seq: 1,
    sessionId: SESSION_ID,
    sessionEvidence: 'header:X-Claude-Code-Session-Id',
    requestId: 'req_011',
    method: 'POST',
    path: '/v1/messages',
    query: 'beta=true',
    kind: 'model_inference',
    state: 'complete',
    status: 200,
    requestedModel: 'claude-sonnet-4-5',
    responseModel: 'claude-sonnet-4-5-20250929',
    stream: true,
    stopReason: 'tool_use',
    messageId: 'msg_01',
    retryCount: 0,
    startedAt: '2026-11-20T14:00:01Z',
    durationMs: 2_500,
    ttfbMs: 400,
    firstEventMs: 420,
    requestBytes: 50_000,
    responseBytes: 4_000,
    sseEvents: 12,
    headline: 'O modelo respondeu com texto e pediu a ferramenta Read.',
    toolUses: ['Read'],
    toolResults: 0,
    degraded: false,
    captureMode: 'safe',
    usage: usageFixture(),
    ...overrides,
  }
}

/** One exchange of each kind and outcome a session can have */
export function mixedExchanges(): HTTPExchange[] {
  return [
    exchangeFixture(),
    exchangeFixture({
      id: 'ex-2', seq: 2, kind: 'token_count', path: '/v1/messages/count_tokens', stream: false, sseEvents: 0, stopReason: undefined,
      headline: 'Contagem de tokens da entrada: 20.012 tokens estimados.', toolUses: [],
      usage: usageFixture({ countTokens: 20_012, usage: {}, totals: { complete: false }, costUsd: undefined, costStatus: 'unavailable' }),
    }),
    exchangeFixture({
      id: 'ex-3', seq: 3, status: 429, state: 'complete', stream: false, sseEvents: 0, retryCount: 1, toolUses: [],
      headline: 'A API recusou a requisição: rate_limit_error.', errorType: 'rate_limit_error', usage: undefined,
    }),
    exchangeFixture({
      id: 'ex-4', seq: 4, status: 529, stream: false, sseEvents: 0, toolUses: [],
      headline: 'A API está sobrecarregada: overloaded_error.', errorType: 'overloaded_error', usage: undefined,
    }),
    exchangeFixture({
      id: 'ex-5', seq: 5, kind: 'other_api', method: 'GET', path: '/v1/models', query: undefined, stream: false, sseEvents: 0,
      toolUses: [], headline: 'Outro endpoint da API: GET /v1/models.', usage: undefined, requestedModel: undefined, responseModel: undefined,
    }),
    exchangeFixture({
      id: 'ex-6', seq: 6, state: 'partial', degraded: true, toolUses: [], toolResults: 1,
      headline: 'O stream terminou sem message_stop: resposta parcial.',
      usage: usageFixture({ quality: 'partial', costStatus: 'partial' }),
    }),
    exchangeFixture({
      id: 'ex-7', seq: 7, kind: 'transport_error', status: 0, state: 'failed', stream: false, sseEvents: 0, toolUses: [],
      headline: 'A conexão com a API falhou antes da resposta.', error: 'dial tcp: connection refused', usage: undefined,
      responseModel: undefined, stopReason: undefined, ttfbMs: undefined, firstEventMs: undefined, responseBytes: 0,
    }),
  ]
}

export function summaryFixture(overrides: Partial<HTTPTrafficSummary> = {}): HTTPTrafficSummary {
  const tokens = {
    input: 12, output: 340, cacheRead: 18_000, cacheWrite: 2_000, cacheWrite5m: 1_500, cacheWrite1h: 500, cacheWriteOther: 0,
    totalInput: 20_012, totalProcessed: 20_352, cacheReadShare: 0.8995, exchanges: 1,
  }
  return {
    exchanges: 7, inference: 4, tokenCounts: 1, other: 2, errors: 3, retries: 1, partial: 1, inProgress: 0, streams: 2,
    tokens,
    partialTokens: { ...tokens, exchanges: 1 },
    countedTokens: 20_012,
    costUsd: 0.0123,
    costStatus: 'partial',
    durationMs: 245_000,
    firstAt: '2026-11-20T14:00:01Z',
    lastAt: '2026-11-20T14:04:06Z',
    models: ['claude-sonnet-4-5-20250929'],
    degraded: 1,
    ...overrides,
  }
}

const EMPTY_TOKENS = {
  input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cacheWrite5m: 0, cacheWrite1h: 0, cacheWriteOther: 0, totalInput: 0, totalProcessed: 0, exchanges: 0,
}

/** What GET /api/sessions/{id}/http-exchanges answers for a session without captures */
export function emptyTrafficFixture(sessionId = SESSION_ID): HTTPSessionTrafficResponse {
  return {
    sessionId,
    exchanges: [],
    total: 0,
    hasMore: false,
    runs: [],
    summary: {
      exchanges: 0, inference: 0, tokenCounts: 0, other: 0, errors: 0, retries: 0, partial: 0, inProgress: 0, streams: 0,
      tokens: EMPTY_TOKENS, partialTokens: EMPTY_TOKENS, countedTokens: 0, costUsd: 0, costStatus: 'unavailable', durationMs: 0,
      models: [], degraded: 0,
    },
  }
}

export function trafficFixture(exchanges = mixedExchanges(), overrides: Partial<HTTPSessionTrafficResponse> = {}): HTTPSessionTrafficResponse {
  return {
    sessionId: SESSION_ID,
    exchanges,
    total: exchanges.length,
    hasMore: false,
    runs: [runFixture()],
    summary: summaryFixture(),
    comparison: {
      selected: 'telemetry',
      proxyInputTokens: 20_012,
      proxyOutputTokens: 340,
      matchedRequests: 1,
      certainty: 'partial',
      note: 'Parte das requisições capturadas também está na telemetria (mesmo request_id).',
    },
    ...overrides,
  }
}

export function detailFixture(exchange = exchangeFixture(), overrides: Partial<HTTPExchangeDetail> = {}): HTTPExchangeDetail {
  return {
    exchange,
    lifecycle: [
      { event: 'request_started', at: '2026-11-20T14:00:01.000Z' },
      { event: 'response_headers_received', at: '2026-11-20T14:00:01.400Z' },
      { event: 'completed', at: '2026-11-20T14:00:03.500Z' },
    ],
    observations: [
      {
        code: 'tool_result_submitted', side: 'request', certainty: 'observed',
        text: 'A requisição devolveu 1 resultado de ferramenta ao modelo.', evidence: 'request.messages[].content[].type=tool_result (1)',
      },
      {
        code: 'tool_use', side: 'response', certainty: 'observed',
        text: 'O modelo pediu a ferramenta Read.', evidence: 'response.content[1].type=tool_use, response.content[1].name=Read',
      },
      {
        code: 'context_change', side: 'request', certainty: 'inferred',
        text: 'O histórico cresceu em 2 mensagens desde a requisição anterior.', evidence: 'messages 4 → 6',
      },
    ],
    degradedReasons: [],
    nextId: 'ex-2',
    run: runFixture(),
    ...overrides,
  }
}

export function requestSummaryFixture(overrides: Partial<WireRequestSummary> = {}): WireRequestSummary {
  return {
    valid: true,
    model: 'claude-sonnet-4-5',
    maxTokens: 32_000,
    stream: true,
    thinking: { type: 'enabled', budget_tokens: 4000 },
    system: { present: true, kind: 'blocks', blocks: 2, bytes: 1_200, sha256: 'ab'.repeat(32) },
    tools: {
      count: 2, deferred: 1, bytes: 3_000, sha256: 'cd'.repeat(32),
      tools: [
        { name: 'Read', bytes: 1_000, sha256: 'ef'.repeat(32) },
        { name: 'WebSearch', type: 'web_search_20250305', deferLoading: true, bytes: 2_000, sha256: '01'.repeat(32) },
      ],
    },
    messages: { count: 2, roles: { user: 1, assistant: 1 }, blocks: { text: 2 }, bytes: 400, hashes: ['aaaa', 'bbbb'], sizes: [200, 200] },
    toolResults: [],
    toolUses: [],
    cacheBreakpoints: [{ location: 'system[1]', ttl: '1h', type: 'ephemeral' }],
    unknownFields: [],
    ...overrides,
  }
}

export function requestBodyFixture(overrides: Partial<HTTPExchangeBody> = {}): HTTPExchangeBody {
  const body = {
    model: 'claude-sonnet-4-5',
    max_tokens: 32000,
    stream: true,
    system: [
      { type: 'text', text: 'Você é o Claude Code.' },
      { type: 'text', text: 'Instruções do projeto.', cache_control: { type: 'ephemeral', ttl: '1h' } },
    ],
    tools: [{ name: 'Read', input_schema: { type: 'object' } }, { name: 'WebSearch', type: 'web_search_20250305' }],
    messages: [
      { role: 'user', content: 'Leia o arquivo main.go' },
      { role: 'assistant', content: [{ type: 'text', text: 'Vou ler o arquivo.' }] },
    ],
  }
  return {
    exchangeId: 'ex-1',
    part: 'request',
    headers: [
      { name: 'content-type', value: 'application/json', redacted: false },
      { name: 'x-api-key', value: '[REDACTED]', redacted: true },
    ],
    variant: 'safe',
    available: ['safe'],
    contentType: 'application/json',
    decodeStatus: 'identity',
    observedBytes: 50_000,
    storedBytes: 50_000,
    truncated: false,
    sanitized: true,
    redactions: 2,
    bodyEncoding: 'utf8',
    body: JSON.stringify(body),
    request: requestSummaryFixture(),
    ...overrides,
  }
}

export function responseBodyFixture(overrides: Partial<HTTPExchangeBody> = {}): HTTPExchangeBody {
  return {
    exchangeId: 'ex-1',
    part: 'response',
    headers: [{ name: 'request-id', value: 'req_011', redacted: false }],
    variant: 'safe',
    available: ['safe'],
    contentType: 'text/event-stream',
    observedBytes: 4_000,
    storedBytes: 4_000,
    truncated: false,
    sanitized: false,
    redactions: 0,
    bodyEncoding: 'utf8',
    body: 'event: message_start\ndata: {"type":"message_start"}\n\n',
    message: {
      id: 'msg_01',
      model: 'claude-sonnet-4-5-20250929',
      role: 'assistant',
      content: [
        { index: 0, type: 'text', text: 'Vou ler o arquivo main.go.', stopped: true },
        { index: 1, type: 'tool_use', id: 'toolu_01', name: 'Read', input: { file_path: 'main.go' }, stopped: true },
      ],
      stopReason: 'tool_use',
      usage: usageFixture().usage,
      streamed: true,
      sawMessageStart: true,
      sawMessageStop: true,
      complete: true,
      eventCounts: { message_start: 1, content_block_delta: 8, message_stop: 1 },
    },
    ...overrides,
  }
}

export function sseEvent(seq: number, overrides: Partial<HTTPSSEEvent> = {}): HTTPSSEEvent {
  const data = JSON.stringify({ type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: `parte ${seq}` } })
  return {
    seq,
    offsetMs: 400 + seq * 10,
    byteOffset: seq * 100,
    byteLen: 100,
    event: 'content_block_delta',
    type: 'content_block_delta',
    index: 0,
    deltaType: 'text_delta',
    parseStatus: 'ok',
    data,
    dataBytes: data.length,
    truncated: false,
    ...overrides,
  }
}

export function eventsFixture(events: HTTPSSEEvent[], overrides: Partial<HTTPSSEEventsResponse> = {}): HTTPSSEEventsResponse {
  const types: Record<string, number> = {}
  for (const e of events) types[e.type] = (types[e.type] ?? 0) + 1
  return { events, total: events.length, hasMore: false, types, ...overrides }
}

export function usageDetailFixture(overrides: Partial<HTTPUsageDetail> = {}): HTTPUsageDetail {
  return {
    ...usageFixture(),
    exchangeId: 'ex-1',
    kind: 'model_inference',
    model: 'claude-sonnet-4-5-20250929',
    snapshots: [
      { eventSeq: 1, eventType: 'message_start', usage: { inputTokens: 12, outputTokens: 1, cacheReadInputTokens: 18_000, cacheCreationInputTokens: 2_000 } },
      { eventSeq: 12, eventType: 'message_delta', usage: { inputTokens: 12, outputTokens: 340, cacheReadInputTokens: 18_000, cacheCreationInputTokens: 2_000 } },
    ],
    costComponents: [
      { component: 'input', tokens: 12, pricePerMTok: 3, costUsd: 0.000036 },
      { component: 'output', tokens: 340, pricePerMTok: 15, costUsd: 0.0051 },
      { component: 'cache_read', tokens: 18_000, pricePerMTok: 0.3, costUsd: 0.0054 },
      { component: 'cache_write_5m', tokens: 1_500, pricePerMTok: 3.75, costUsd: 0.005625 },
      { component: 'cache_write_1h', tokens: 500, pricePerMTok: 6, costUsd: 0.003 },
    ],
    ...overrides,
  }
}

function diff(overrides: Partial<WireRequestDiff> = {}): WireRequestDiff {
  return {
    modelBefore: 'claude-sonnet-4-5', modelAfter: 'claude-sonnet-4-5', modelChanged: false,
    paramChanges: [{ field: 'max_tokens', before: '16000', after: '32000' }],
    systemChanged: false, systemBytesBefore: 1_200, systemBytesAfter: 1_200,
    toolsChanged: true, toolsBytesBefore: 2_000, toolsBytesAfter: 3_000,
    toolsAdded: ['WebSearch'], toolsRemoved: [], toolsModified: [], toolsCountBefore: 1, toolsCountAfter: 2,
    messagesBefore: 4, messagesAfter: 6, commonPrefix: 4, messagesAdded: 2, messagesReplaced: 0,
    newBytes: 800, repeatedBytes: 1_600, newToolResults: ['toolu_00'],
    cacheBreakpointsBefore: [{ location: 'system[1]' }], cacheBreakpointsAfter: [{ location: 'system[1]' }, { location: 'messages[5].content[0]', ttl: '5m' }],
    cacheChanged: true, possibleCompaction: false,
    ...overrides,
  }
}

export function diffFixture(overrides: Partial<HTTPRequestDiffResponse> = {}): HTTPRequestDiffResponse {
  return { exchangeId: 'ex-1', previousId: 'ex-0', previousSeq: 0, comparedBy: 'same_conversation', diff: diff(), ...overrides }
}

export function firstDiffFixture(): HTTPRequestDiffResponse {
  return {
    exchangeId: 'ex-1',
    comparedBy: 'none',
    diff: diff({ messagesBefore: 0, messagesAfter: 1, commonPrefix: 0, messagesAdded: 1, newBytes: 200, repeatedBytes: 0, paramChanges: [], toolsAdded: [] }),
  }
}

export function toolsFixture(overrides: Partial<HTTPToolLinksResponse> = {}): HTTPToolLinksResponse {
  return {
    note: 'O proxy vê apenas o pedido de ferramenta (tool_use) na resposta do modelo e o resultado (tool_result) enviado no request seguinte. A execução local da ferramenta não passa pela rede e não é observada.',
    tools: [
      {
        toolUseId: 'toolu_01', name: 'Read', sessionId: SESSION_ID, useExchangeId: 'ex-1', useSeq: 1,
        input: '{"file_path":"main.go"}', resultExchangeId: 'ex-6', resultSeq: 6, resultIsError: false,
        resultPreview: 'package main', resultBytes: 1_024, status: 'matched', evidence: 'tool_result com o mesmo tool_use_id',
      },
      {
        toolUseId: 'toolu_02', name: 'Bash', sessionId: SESSION_ID, useExchangeId: 'ex-6', useSeq: 6,
        input: '{"command":"go test ./..."}', resultExchangeId: 'ex-7', resultSeq: 7, resultIsError: true,
        resultPreview: 'FAIL', resultBytes: 64, status: 'matched', evidence: 'tool_result com o mesmo tool_use_id',
      },
      {
        toolUseId: 'toolu_03', name: 'Edit', sessionId: SESSION_ID, useExchangeId: 'ex-7', useSeq: 7, resultIsError: false,
        resultBytes: 0, status: 'pending', evidence: 'tool_use observado; nenhum request posterior trouxe o resultado ainda',
      },
    ],
    ...overrides,
  }
}
