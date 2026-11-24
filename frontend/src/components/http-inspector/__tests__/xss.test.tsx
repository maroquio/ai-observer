import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent, { type UserEvent } from '@testing-library/user-event'
import { api } from '@/lib/api'
import { preloadHttpInspectorPages, renderApp } from '@/test/renderApp'
import { statusFixture } from '@/test/statusFixture'
import {
  SESSION_ID,
  detailFixture,
  eventsFixture,
  exchangeFixture,
  requestBodyFixture,
  responseBodyFixture,
  sseEvent,
  toolsFixture,
  trafficFixture,
} from '@/test/httpInspectorFixtures'
import type { HTTPBodyPart, HTTPToolLink } from '@/types/httpInspector'

vi.mock('@/lib/api', () => ({
  api: {
    getProviders: vi.fn(),
    getDashboards: vi.fn(),
    getStatus: vi.fn(),
    getSessionTranscript: vi.fn(),
    getSessionHTTPExchanges: vi.fn(),
    getHTTPRunTools: vi.fn(),
    getHTTPExchange: vi.fn(),
    getHTTPExchangeBody: vi.fn(),
    getHTTPExchangeEvents: vi.fn(),
  },
}))

// Captured payloads are untrusted: whatever a prompt, a reply, a stream
// event or a tool result contains must show as text, never become markup,
// links or loaded resources (AC-15).
const IMG = '<img src=x onerror=alert(1)>'
const SCRIPT = '<script>alert(1)</script>'
const MD_LINK = '[x](javascript:alert(1))'
const PAYLOAD = `${IMG} ${SCRIPT} ${MD_LINK}`
const HOSTILE_SESSION = 'javascript:alert(1)'

function hostileRequestBody(): string {
  return JSON.stringify({
    model: 'claude-sonnet-4-5',
    max_tokens: 1024,
    system: [{ type: 'text', text: PAYLOAD }],
    tools: [{ name: PAYLOAD, description: PAYLOAD, input_schema: { type: 'object' } }],
    messages: [
      { role: 'user', content: PAYLOAD },
      {
        role: 'assistant',
        content: [
          { type: 'text', text: PAYLOAD },
          { type: 'tool_use', id: 'toolu_x', name: 'Read', input: { file_path: PAYLOAD } },
        ],
      },
      {
        role: 'user',
        content: [
          { type: 'tool_result', tool_use_id: 'toolu_x', content: PAYLOAD },
          { type: 'image', source: { type: 'url', url: 'javascript:alert(1)' } },
          { type: 'document', source: { type: 'url', url: 'https://example.invalid/x.pdf' } },
          { type: 'text', text: MD_LINK },
        ],
      },
    ],
  })
}

const hostileTools: HTTPToolLink[] = [
  {
    toolUseId: 'toolu_x', name: PAYLOAD, sessionId: SESSION_ID, useExchangeId: 'ex-1', useSeq: 1,
    input: JSON.stringify({ file_path: PAYLOAD }), resultExchangeId: 'ex-2', resultSeq: 2, resultIsError: false,
    resultPreview: PAYLOAD, resultBytes: 128, status: 'matched', evidence: 'tool_result com o mesmo tool_use_id',
  },
]

/** No markup, script, loaded resource or javascript: link came from the payload */
function expectInert(root: HTMLElement) {
  expect(root.querySelector('img')).toBeNull()
  expect(root.querySelector('script')).toBeNull()
  expect(root.querySelector('iframe, object, embed')).toBeNull()
  expect(root.querySelector('[onerror]')).toBeNull()
  expect(root.querySelector('[src]')).toBeNull()
  for (const a of root.querySelectorAll('a')) {
    // Every link is one of the app's own paths, built from encoded IDs
    expect(a.getAttribute('href') ?? '').toMatch(/^\//)
    expect(a.getAttribute('href') ?? '').not.toMatch(/^\s*javascript:/i)
    expect(a.getAttribute('href') ?? '').not.toContain('example.invalid')
  }
  expect(window.alert).not.toHaveBeenCalled()
}

/** Shown literally, as typed */
function expectLiteral(root: HTMLElement) {
  expect(root.textContent).toContain(IMG)
  expect(root.textContent).toContain(SCRIPT)
  expect(root.textContent).toContain(MD_LINK)
}

/** Opens every collapsed section of the page's main area */
async function expandAll(user: UserEvent, root: HTMLElement) {
  for (let round = 0; round < 10; round++) {
    const closed = within(root)
      .queryAllByRole('button')
      .filter((b) => b.getAttribute('aria-expanded') === 'false')
    if (closed.length === 0) return
    for (const button of closed) await user.click(button)
  }
}

// The page's content (the layout's <main>, inside the sidebar inset's)
function main(): HTMLElement {
  return screen.getAllByRole('main').at(-1)!
}

describe('HTTP Inspector renders captured payloads as text only', () => {
  beforeAll(() => preloadHttpInspectorPages(), 20_000)

  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.mocked(api.getProviders).mockResolvedValue({ providers: [] })
    vi.mocked(api.getDashboards).mockResolvedValue({ dashboards: [] })
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture())

    // The session ID comes from a request header or the body: untrusted too
    const exchange = exchangeFixture({ sessionId: HOSTILE_SESSION, headline: PAYLOAD, toolUses: [PAYLOAD], error: PAYLOAD })
    vi.mocked(api.getHTTPExchange).mockResolvedValue(
      detailFixture(exchange, {
        observations: [{ code: 'tool_use', side: 'response', certainty: 'observed', text: PAYLOAD, evidence: PAYLOAD }],
        degradedReasons: [PAYLOAD],
      }),
    )
    vi.mocked(api.getHTTPExchangeBody).mockImplementation(async (_id: string, part: HTTPBodyPart) =>
      part === 'request'
        ? requestBodyFixture({ body: hostileRequestBody(), headers: [{ name: 'x-note', value: PAYLOAD, redacted: false }] })
        : responseBodyFixture({
            body: `data: ${JSON.stringify({ text: PAYLOAD })}\n\n`,
            message: {
              ...responseBodyFixture().message!,
              content: [
                { index: 0, type: 'text', text: PAYLOAD, stopped: true },
                { index: 1, type: 'tool_use', id: 'toolu_x', name: PAYLOAD, input: { file_path: PAYLOAD }, stopped: true },
                { index: 2, type: 'thinking', thinking: PAYLOAD, stopped: true },
              ],
              notes: [PAYLOAD],
              error: { type: PAYLOAD, message: PAYLOAD },
            },
          }),
    )
    vi.mocked(api.getHTTPExchangeEvents).mockResolvedValue(
      eventsFixture([
        sseEvent(1, { data: JSON.stringify({ type: 'content_block_delta', delta: { type: 'text_delta', text: PAYLOAD } }) }),
        sseEvent(2, { type: PAYLOAD, event: PAYLOAD, data: PAYLOAD, parseStatus: 'invalid_json' }),
      ]),
    )
    vi.mocked(api.getSessionTranscript).mockResolvedValue({
      sessionId: SESSION_ID, serviceName: 'claude-code', startTime: '2026-11-20T14:00:00Z', lastTime: '2026-11-20T14:05:00Z', messages: [], total: 0, hasMore: false,
    })
    vi.mocked(api.getSessionHTTPExchanges).mockResolvedValue(trafficFixture([exchange]))
    vi.mocked(api.getHTTPRunTools).mockResolvedValue(toolsFixture({ tools: hostileTools }))
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('in the summary', async () => {
    const user = userEvent.setup()
    renderApp(`/sessions/${SESSION_ID}/http-exchanges/ex-1`)
    await screen.findByRole('heading', { name: 'Troca #1' })
    await expandAll(user, main())
    expectLiteral(main())
    expectInert(main())
    for (const link of within(main()).getAllByRole('link', { name: new RegExp(HOSTILE_SESSION.replace(/[()]/g, '\\$&')) })) {
      expect(link).toHaveAttribute('href', '/sessions/javascript%3Aalert(1)?view=traffic')
    }
  })

  it('in the request, structured and as original JSON', async () => {
    const user = userEvent.setup()
    renderApp(`/sessions/${SESSION_ID}/http-exchanges/ex-1?tab=request`)
    await screen.findByRole('list', { name: 'Mensagens da requisição' })
    await expandAll(user, main())
    expectLiteral(main())
    expectInert(main())
    // Images and documents are described, never loaded
    expect(within(main()).getAllByText(/não exibid/).length).toBeGreaterThan(0)

    await user.click(screen.getByRole('button', { name: 'JSON original' }))
    const body = screen.getByLabelText('Corpo da requisição')
    expect(body.tagName).toBe('PRE')
    expectLiteral(body)
    expectInert(main())
  })

  it('in the response', async () => {
    const user = userEvent.setup()
    renderApp(`/sessions/${SESSION_ID}/http-exchanges/ex-1?tab=response`)
    await screen.findByText('Eventos do stream')
    await expandAll(user, main())
    expectLiteral(main())
    expectInert(main())
  })

  it('in the SSE stream', async () => {
    const user = userEvent.setup()
    renderApp(`/sessions/${SESSION_ID}/http-exchanges/ex-1?tab=sse`)
    await user.click(await screen.findByRole('button', { name: 'Evento 1: content_block_delta' }))
    expectLiteral(screen.getByLabelText('Dados do evento 1'))
    await user.click(screen.getByRole('button', { name: `Evento 2: ${PAYLOAD}` }))
    expectLiteral(screen.getByLabelText('Dados do evento 2'))
    expectInert(main())
  })

  it('in the session timeline and the tool links', async () => {
    const user = userEvent.setup()
    renderApp(`/sessions/${SESSION_ID}?view=traffic`)
    await screen.findByRole('table', { name: 'Linha do tempo das trocas' })
    expect(main().textContent).toContain(IMG)
    expectInert(main())

    await user.click(screen.getByRole('tab', { name: 'Ferramentas (tool_use → tool_result)' }))
    await screen.findByRole('list', { name: 'Ferramentas pedidas e resultados' })
    await expandAll(user, main())
    expect(screen.getByLabelText('Entrada da ferramenta')).toHaveTextContent(IMG)
    expectLiteral(screen.getByLabelText('Resultado da ferramenta'))
    expectInert(main())
  })
})
