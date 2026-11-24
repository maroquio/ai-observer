import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { SessionTranscriptPage } from '@/pages/SessionTranscriptPage'
import { HttpExchangePage } from '@/pages/HttpExchangePage'
import { api, type HTTPExchangeQuery } from '@/lib/api'
import { createTestQueryClient } from '@/test/queryWrapper'
import {
  SESSION_ID,
  detailFixture,
  eventsFixture,
  exchangeFixture,
  mixedExchanges,
  toolsFixture,
  trafficFixture,
} from '@/test/httpInspectorFixtures'
import type { TranscriptResponse } from '@/types/sessions'

vi.mock('@/lib/api', () => ({
  api: {
    getSessionTranscript: vi.fn(),
    getSessionHTTPExchanges: vi.fn(),
    getHTTPRunTools: vi.fn(),
    getHTTPExchange: vi.fn(),
    getHTTPExchangeEvents: vi.fn(),
  },
}))

const transcript: TranscriptResponse = {
  sessionId: SESSION_ID,
  serviceName: 'claude-code',
  startTime: '2026-11-20T14:00:00Z',
  lastTime: '2026-11-20T14:05:00Z',
  messages: [{ timestamp: '2026-11-20T14:00:00Z', role: 'user', content: 'Leia o arquivo main.go', index: 0 }],
  total: 1,
  hasMore: false,
}

// Answers like the server: the summary does not depend on the filters,
// the exchange list does (kind and search are enough here)
function serveTraffic(exchanges = mixedExchanges()) {
  vi.mocked(api.getSessionHTTPExchanges).mockImplementation(async (_sessionId, params: HTTPExchangeQuery = {}) => {
    let rows = exchanges
    if (params.kind) rows = rows.filter((e) => e.kind === params.kind)
    if (params.search) rows = rows.filter((e) => `${e.headline} ${e.path}`.toLowerCase().includes(params.search!.toLowerCase()))
    if (params.status === 'error') rows = rows.filter((e) => e.status >= 400 || e.status === 0)
    return trafficFixture(rows.slice(0, params.limit ?? 500), { total: rows.length })
  })
}

function renderTraffic(url = `/sessions/${SESSION_ID}?view=traffic`) {
  const router = createMemoryRouter(
    [
      { path: '/sessions/:sessionId', element: <SessionTranscriptPage /> },
      { path: '/sessions/:sessionId/http-exchanges/:exchangeId', element: <HttpExchangePage /> },
    ],
    { initialEntries: [url] },
  )
  render(
    <QueryClientProvider client={createTestQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

async function timelineRow(seq: number) {
  const link = await screen.findByRole('link', { name: new RegExp(`^Troca #${seq}:`) })
  return link.closest('[role="row"]') as HTMLElement
}

describe('Session API traffic tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    vi.mocked(api.getSessionTranscript).mockResolvedValue(transcript)
    vi.mocked(api.getHTTPRunTools).mockResolvedValue(toolsFixture())
    vi.mocked(api.getHTTPExchange).mockResolvedValue(detailFixture())
    vi.mocked(api.getHTTPExchangeEvents).mockResolvedValue(eventsFixture([]))
    serveTraffic()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('summarizes the captured traffic, keeping the telemetry comparison apart', async () => {
    renderTraffic()
    expect(await screen.findByText('Trocas')).toBeInTheDocument()
    expect(screen.getByText('Erros, retries e parciais')).toBeInTheDocument()
    expect(screen.getByText('Tokens (usage final)')).toBeInTheDocument()
    expect(screen.getByText(/5m: 1,500 · 1h: 500/)).toBeInTheDocument()
    expect(screen.getByText('Custo equivalente de API (estimativa)')).toBeInTheDocument()
    expect(screen.getByText('Vínculo com a sessão')).toBeInTheDocument()
    expect(screen.getByText('Comparação com a telemetria (OTLP / arquivo de sessão)')).toBeInTheDocument()
    expect(screen.getByText('não somado')).toBeInTheDocument()
    // The run that carried the session
    expect(screen.getByRole('link', { name: /Aula 3: refatoração/ })).toHaveAttribute('href', '/http-inspector/runs/run-1')
  })

  it('lists every kind of exchange with its labels', async () => {
    renderTraffic()
    expect(await screen.findByRole('table', { name: 'Linha do tempo das trocas' })).toBeInTheDocument()
    expect(screen.getByRole('status', { hidden: false, name: '' })).toHaveTextContent('7 de 7 trocas')

    const inference = await timelineRow(1)
    expect(within(inference).getByText('Inferência')).toBeInTheDocument()
    expect(within(inference).getByText('200')).toBeInTheDocument()
    expect(within(inference).getByText('SSE')).toBeInTheDocument()
    expect(within(inference).getByText('tool_use: Read')).toBeInTheDocument()

    const countTokens = await timelineRow(2)
    expect(within(countTokens).getByText('Contagem de tokens')).toBeInTheDocument()
    expect(within(countTokens).getByText(/≈/)).toBeInTheDocument()
    // count_tokens has no usage: no "unavailable" flag
    expect(within(countTokens).queryByText('usage indisponível')).not.toBeInTheDocument()

    const rateLimited = await timelineRow(3)
    expect(within(rateLimited).getByText('429')).toBeInTheDocument()
    expect(within(rateLimited).getByText('retry 1')).toBeInTheDocument()
    expect(within(rateLimited).getByText('usage indisponível')).toBeInTheDocument()

    const overloaded = await timelineRow(4)
    expect(within(overloaded).getByText('529')).toBeInTheDocument()

    const other = await timelineRow(5)
    expect(within(other).getByText('Outro endpoint')).toBeInTheDocument()
    expect(within(other).queryByText('usage indisponível')).not.toBeInTheDocument()

    const partial = await timelineRow(6)
    expect(within(partial).getByText('Parcial')).toBeInTheDocument()
    expect(within(partial).getByText('usage parcial')).toBeInTheDocument()
    expect(within(partial).getByText('captura degradada')).toBeInTheDocument()
    expect(within(partial).getByText('1 tool_result')).toBeInTheDocument()

    const transport = await timelineRow(7)
    expect(within(transport).getByText('Erro de transporte')).toBeInTheDocument()
    expect(within(transport).getByText('sem resposta')).toBeInTheDocument()
    expect(within(transport).getByText('Falhou (sem resposta)')).toBeInTheDocument()
  })

  it('opens an exchange at its own URL', async () => {
    const user = userEvent.setup()
    const router = renderTraffic()
    await user.click(await screen.findByRole('link', { name: 'Troca #1: POST /v1/messages?beta=true' }))
    expect(router.state.location.pathname).toBe(`/sessions/${SESSION_ID}/http-exchanges/ex-1`)
    expect(await screen.findByRole('heading', { name: /Troca #1/ })).toBeInTheDocument()
    expect(api.getHTTPExchange).toHaveBeenCalledWith('ex-1', expect.anything())

    // Back goes to the traffic tab the exchange was opened from
    await user.click(screen.getByRole('link', { name: /Voltar/ }))
    expect(router.state.location.pathname).toBe(`/sessions/${SESSION_ID}`)
    expect(router.state.location.search).toBe('?view=traffic')
  })

  it('filters the exchanges in the URL', async () => {
    const user = userEvent.setup()
    const router = renderTraffic()
    await screen.findByRole('table', { name: 'Linha do tempo das trocas' })

    await user.selectOptions(screen.getByRole('combobox', { name: 'Tipo de troca' }), 'token_count')
    expect(new URLSearchParams(router.state.location.search).get('kind')).toBe('token_count')
    await waitFor(() => expect(screen.queryByRole('link', { name: /^Troca #1:/ })).not.toBeInTheDocument())
    expect(screen.getByRole('link', { name: /^Troca #2:/ })).toBeInTheDocument()
    expect(api.getSessionHTTPExchanges).toHaveBeenLastCalledWith(
      SESSION_ID,
      expect.objectContaining({ kind: 'token_count', limit: 500 }),
      expect.anything(),
    )
    // The summary counts all exchanges, whatever the filters
    expect(screen.getByText('Trocas')).toBeInTheDocument()

    await user.selectOptions(screen.getByRole('combobox', { name: 'Tipo de troca' }), '')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Status HTTP' }), 'error')
    expect(new URLSearchParams(router.state.location.search).get('status')).toBe('error')
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('3 de 3 trocas que correspondem aos filtros'))
  })

  it('searches the exchanges and offers to clear filters that match nothing', async () => {
    const user = userEvent.setup()
    const router = renderTraffic()
    await screen.findByRole('table', { name: 'Linha do tempo das trocas' })

    await user.type(screen.getByRole('searchbox', { name: 'Buscar trocas' }), 'sobrecarregada')
    await waitFor(() =>
      expect(api.getSessionHTTPExchanges).toHaveBeenLastCalledWith(SESSION_ID, expect.objectContaining({ search: 'sobrecarregada' }), expect.anything()),
    )
    expect(new URLSearchParams(router.state.location.search).get('search')).toBe('sobrecarregada')
    await waitFor(() => expect(screen.queryByRole('link', { name: /^Troca #1:/ })).not.toBeInTheDocument())
    expect(screen.getByRole('link', { name: /^Troca #4:/ })).toBeInTheDocument()

    await user.clear(screen.getByRole('searchbox', { name: 'Buscar trocas' }))
    await user.type(screen.getByRole('searchbox', { name: 'Buscar trocas' }), 'nada disso')
    expect(await screen.findByText('Nenhuma troca corresponde aos filtros')).toBeInTheDocument()
    await user.click(screen.getAllByRole('button', { name: 'Limpar filtros' })[0])
    expect(await screen.findByRole('link', { name: /^Troca #1:/ })).toBeInTheDocument()
    expect(router.state.location.search).toBe('?view=traffic')
  })

  it('restores the filters from the URL', async () => {
    renderTraffic(`/sessions/${SESSION_ID}?view=traffic&kind=other_api`)
    expect(await screen.findByRole('link', { name: /^Troca #5:/ })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Tipo de troca' })).toHaveValue('other_api')
    expect(screen.queryByRole('link', { name: /^Troca #1:/ })).not.toBeInTheDocument()
  })

  it('links tool uses to their results, saying the local execution is not observed', async () => {
    const user = userEvent.setup()
    const router = renderTraffic()
    await user.click(await screen.findByRole('tab', { name: 'Ferramentas (tool_use → tool_result)' }))
    expect(new URLSearchParams(router.state.location.search).get('section')).toBe('tools')
    expect(await screen.findByText('A execução local não é observada pelo proxy')).toBeInTheDocument()
    expect(api.getHTTPRunTools).toHaveBeenCalledWith('run-1', expect.anything())
    expect(screen.getByText('toolu_01')).toBeInTheDocument()
  })

  it('marks exchanges whose usage never arrived', async () => {
    serveTraffic([exchangeFixture({ id: 'ex-9', seq: 9, state: 'aborted', usage: { quality: 'unavailable', usage: {}, totals: { complete: false }, costStatus: 'unavailable', source: 'http_proxy' } })])
    renderTraffic()
    const row = await timelineRow(9)
    expect(within(row).getByText('Abortada pelo cliente')).toBeInTheDocument()
    expect(within(row).getByText('usage indisponível')).toBeInTheDocument()
  })
})
