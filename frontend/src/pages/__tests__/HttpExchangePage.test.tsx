import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { api } from '@/lib/api'
import { httpError } from '@/lib/errors'
import { preloadHttpInspectorPages, renderApp } from '@/test/renderApp'
import { statusFixture } from '@/test/statusFixture'
import {
  SESSION_ID,
  detailFixture,
  diffFixture,
  eventsFixture,
  exchangeFixture,
  firstDiffFixture,
  requestBodyFixture,
  responseBodyFixture,
  sseEvent,
  usageDetailFixture,
} from '@/test/httpInspectorFixtures'
import type { HTTPBodyPart } from '@/types/httpInspector'

vi.mock('@/lib/api', () => ({
  api: {
    getProviders: vi.fn(),
    getDashboards: vi.fn(),
    getStatus: vi.fn(),
    getHTTPExchange: vi.fn(),
    getHTTPExchangeBody: vi.fn(),
    getHTTPExchangeEvents: vi.fn(),
    getHTTPExchangeUsage: vi.fn(),
    getHTTPExchangeDiff: vi.fn(),
  },
}))

// Opens a URL as after a reload: the app's real route table, nothing cached
function openUrl(url: string) {
  return renderApp(url).router
}

const EXCHANGE_URL = `/sessions/${SESSION_ID}/http-exchanges/ex-1`

describe('HTTP exchange inspector', () => {
  beforeAll(() => preloadHttpInspectorPages(), 20_000)

  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    vi.mocked(api.getProviders).mockResolvedValue({ providers: [] })
    vi.mocked(api.getDashboards).mockResolvedValue({ dashboards: [] })
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture())
    vi.mocked(api.getHTTPExchange).mockResolvedValue(detailFixture(exchangeFixture(), { prevId: 'ex-0' }))
    vi.mocked(api.getHTTPExchangeBody).mockImplementation(async (_id: string, part: HTTPBodyPart) =>
      part === 'request' ? requestBodyFixture() : responseBodyFixture(),
    )
    vi.mocked(api.getHTTPExchangeEvents).mockResolvedValue(
      eventsFixture([sseEvent(1, { type: 'message_start', event: 'message_start', deltaType: undefined }), sseEvent(2), sseEvent(3)]),
    )
    vi.mocked(api.getHTTPExchangeUsage).mockResolvedValue(usageDetailFixture())
    vi.mocked(api.getHTTPExchangeDiff).mockResolvedValue(diffFixture())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('opens a session exchange from its URL, as after a reload', async () => {
    openUrl(EXCHANGE_URL)
    expect(await screen.findByRole('heading', { name: 'Troca #1' })).toBeInTheDocument()
    expect(api.getHTTPExchange).toHaveBeenCalledWith('ex-1', expect.anything())
    expect(document.title).toContain('Troca #1')
    expect(screen.getByText('O modelo respondeu com texto e pediu a ferramenta Read.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Execução: Aula 3: refatoração/ })).toHaveAttribute('href', '/http-inspector/runs/run-1')
    expect(screen.getByRole('link', { name: new RegExp(`Sessão ${SESSION_ID}`) })).toHaveAttribute('href', `/sessions/${SESSION_ID}?view=traffic`)
    // Without a previous page, back goes to the session's traffic
    expect(screen.getByRole('link', { name: 'Voltar à sessão' })).toHaveAttribute('href', `/sessions/${SESSION_ID}?view=traffic`)
    // Neighbours are the run's
    expect(screen.getByRole('link', { name: 'Troca anterior da execução' })).toHaveAttribute('href', '/http-inspector/runs/run-1/exchanges/ex-0')
    expect(screen.getByRole('link', { name: 'Próxima troca da execução' })).toHaveAttribute('href', '/http-inspector/runs/run-1/exchanges/ex-2')
  })

  it('opens a run exchange on the tab in its URL', async () => {
    openUrl('/http-inspector/runs/run-1/exchanges/ex-1?tab=tokens')
    expect(await screen.findByRole('tab', { name: 'Tokens & Cache' })).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByRole('table', { name: 'Campos de usage' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Voltar à execução' })).toHaveAttribute('href', '/http-inspector/runs/run-1')
    // The neighbours keep the tab
    expect(screen.getByRole('link', { name: 'Próxima troca da execução' })).toHaveAttribute('href', '/http-inspector/runs/run-1/exchanges/ex-2?tab=tokens')
  })

  it('says when an exchange does not exist', async () => {
    vi.mocked(api.getHTTPExchange).mockRejectedValue(httpError(404, 'exchange not found'))
    openUrl('/http-inspector/runs/run-1/exchanges/nope')
    expect(await screen.findByText('Troca não encontrada')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('summarizes the exchange: fields, observations with origin and evidence, lifecycle', async () => {
    const user = userEvent.setup()
    openUrl(EXCHANGE_URL)
    expect(await screen.findByRole('tab', { name: 'Resumo' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText('O que foi enviado?')).toBeInTheDocument()
    expect(screen.getByText('O que foi recebido?')).toBeInTheDocument()
    expect(screen.getByText('O modelo pediu a ferramenta Read.')).toBeInTheDocument()
    expect(screen.getAllByText('Inferido').length).toBeGreaterThan(0)

    const evidence = screen.getAllByRole('button', { name: 'Ver evidência' })[0]
    expect(evidence).toHaveAttribute('aria-expanded', 'false')
    await user.click(evidence)
    expect(screen.getByRole('button', { name: 'Ocultar evidência' })).toHaveAttribute('aria-expanded', 'true')

    const lifecycle = screen.getByRole('list', { name: 'Ciclo de vida da troca' })
    expect(within(lifecycle).getByText('Requisição iniciada')).toBeInTheDocument()
    expect(within(lifecycle).getByText('Concluída')).toBeInTheDocument()
  })

  it('moves between the six tabs with the keyboard, keeping the tab in the URL', async () => {
    const user = userEvent.setup()
    const router = openUrl(EXCHANGE_URL)
    const tabs = within(await screen.findByRole('tablist', { name: 'Partes da troca' })).getAllByRole('tab')
    expect(tabs.map((t) => t.textContent)).toEqual(['Resumo', 'Request', 'Response', 'SSE Stream', 'Tokens & Cache', 'Context Diff'])

    tabs[0].focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Request' })).toHaveFocus()
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Request' })).toHaveAttribute('aria-selected', 'true'))
    expect(new URLSearchParams(router.state.location.search).get('tab')).toBe('request')

    await user.keyboard('{End}')
    expect(screen.getByRole('tab', { name: 'Context Diff' })).toHaveFocus()
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Context Diff' })).toHaveAttribute('aria-selected', 'true'))
    expect(new URLSearchParams(router.state.location.search).get('tab')).toBe('diff')
  })

  it('shows the request structured or as the original JSON, with masked values marked', async () => {
    const user = userEvent.setup()
    openUrl(`${EXCHANGE_URL}?tab=request`)
    expect(await screen.findByText('Parâmetros')).toBeInTheDocument()
    expect(api.getHTTPExchangeBody).toHaveBeenCalledWith('ex-1', 'request', 'safe', expect.anything())
    expect(screen.getByText('Você é o Claude Code.')).toBeInTheDocument()
    expect(screen.getByText('2 valores mascarados')).toBeInTheDocument()
    const messages = screen.getByRole('list', { name: 'Mensagens da requisição' })
    expect(within(messages).getByText('Leia o arquivo main.go')).toBeInTheDocument()
    // The credential header is masked, never shown
    const headers = screen.getByRole('table', { name: 'Cabeçalhos' })
    expect(within(headers).getByText('x-api-key')).toBeInTheDocument()
    expect(within(headers).getByText('mascarado')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'JSON original' }))
    expect(screen.getByRole('button', { name: 'JSON original' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText('Corpo da requisição')).toHaveTextContent('"max_tokens": 32000')
    expect(screen.getByRole('button', { name: 'Copiar corpo da requisição' })).toBeInTheDocument()
    // Only the safe variant exists: no raw toggle
    expect(screen.queryByRole('button', { name: /Bruto/ })).not.toBeInTheDocument()
  })

  it('marks the text where a secret was masked before storage', async () => {
    const body = JSON.parse(requestBodyFixture().body!) as { messages: Array<{ role: string; content: unknown }> }
    body.messages[0].content = 'Use a chave [REDACTED:anthropic_key] no teste'
    vi.mocked(api.getHTTPExchangeBody).mockImplementation(async (_id: string, part: HTTPBodyPart) =>
      part === 'request' ? requestBodyFixture({ body: JSON.stringify(body), redactions: 1 }) : responseBodyFixture(),
    )
    openUrl(`${EXCHANGE_URL}?tab=request`)
    const messages = await screen.findByRole('list', { name: 'Mensagens da requisição' })
    expect(within(messages).getByText('Use a chave [REDACTED:anthropic_key] no teste')).toBeInTheDocument()
    expect(within(messages).getByText('segredo mascarado')).toBeInTheDocument()
    expect(screen.getByText('1 valor mascarado')).toBeInTheDocument()
  })

  it('offers the raw body only when full_raw stored one', async () => {
    const user = userEvent.setup()
    vi.mocked(api.getHTTPExchangeBody).mockImplementation(async (_id: string, part: HTTPBodyPart, variant = 'safe') =>
      part === 'request' ? requestBodyFixture({ available: ['safe', 'raw'], variant, sanitized: variant === 'safe' }) : responseBodyFixture(),
    )
    openUrl(`${EXCHANGE_URL}?tab=request`)
    await user.click(await screen.findByRole('button', { name: 'Bruto (full_raw)' }))
    await waitFor(() => expect(api.getHTTPExchangeBody).toHaveBeenCalledWith('ex-1', 'request', 'raw', expect.anything()))
  })

  it('shows the reconstructed response', async () => {
    openUrl(`${EXCHANGE_URL}?tab=response`)
    expect(await screen.findByText('Vou ler o arquivo main.go.')).toBeInTheDocument()
    expect(api.getHTTPExchangeBody).toHaveBeenCalledWith('ex-1', 'response', 'safe', expect.anything())
    expect(screen.getAllByText('Read').length).toBeGreaterThan(0)
    expect(screen.getByText('Eventos do stream')).toBeInTheDocument()
  })

  it('lists the SSE events and shows the one selected', async () => {
    const user = userEvent.setup()
    openUrl(`${EXCHANGE_URL}?tab=sse`)
    expect(await screen.findByRole('status', { name: '' })).toBeInTheDocument()
    expect(screen.getByText('3 de 3 eventos carregados')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Evento 2: content_block_delta' }))
    expect(screen.getByRole('button', { name: 'Evento 2: content_block_delta' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText('Dados do evento 2')).toHaveTextContent('parte 2')

    await user.selectOptions(screen.getByRole('combobox', { name: 'Tipo de evento' }), 'message_start')
    await waitFor(() => expect(api.getHTTPExchangeEvents).toHaveBeenLastCalledWith('ex-1', expect.objectContaining({ type: 'message_start' }), expect.anything()))
  })

  it('says a response without streaming has no events', async () => {
    vi.mocked(api.getHTTPExchange).mockResolvedValue(detailFixture(exchangeFixture({ stream: false, sseEvents: 0 })))
    openUrl(`${EXCHANGE_URL}?tab=sse`)
    expect(await screen.findByText('Resposta sem streaming')).toBeInTheDocument()
    expect(api.getHTTPExchangeEvents).not.toHaveBeenCalledWith('ex-1', expect.objectContaining({ limit: 1000 }), expect.anything())
  })

  it('explains tokens, cache and the estimated cost', async () => {
    openUrl(`${EXCHANGE_URL}?tab=tokens`)
    const fields = await screen.findByRole('table', { name: 'Campos de usage' })
    expect(within(fields).getByText('18,000')).toBeInTheDocument()
    expect(screen.getByText('Totais derivados')).toBeInTheDocument()
    expect(screen.getByRole('table', { name: 'Evolução do usage no stream' })).toBeInTheDocument()
    const cost = screen.getByRole('table', { name: 'Componentes do custo' })
    expect(within(cost).getByText('Escrita de cache (5 min)')).toBeInTheDocument()
    expect(within(cost).getByText('Escrita de cache (1 h)')).toBeInTheDocument()
  })

  it('says when an exchange has no usage', async () => {
    vi.mocked(api.getHTTPExchangeUsage).mockResolvedValue(null)
    openUrl(`${EXCHANGE_URL}?tab=tokens`)
    expect(await screen.findByText('Usage indisponível')).toBeInTheDocument()
  })

  it('compares the request with the previous one of the conversation', async () => {
    openUrl(`${EXCHANGE_URL}?tab=diff`)
    const params = await screen.findByRole('table', { name: 'Parâmetros alterados' })
    expect(within(params).getByText('max_tokens')).toBeInTheDocument()
    expect(within(params).getByText('16000')).toBeInTheDocument()
    expect(within(params).getByText('32000')).toBeInTheDocument()
    expect(screen.getByText('Novos tool_result')).toBeInTheDocument()
    expect(screen.getByText('WebSearch')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /#0/ })).toHaveAttribute('href', `/sessions/${SESSION_ID}/http-exchanges/ex-0`)
  })

  it('says the first request has nothing to compare', async () => {
    vi.mocked(api.getHTTPExchangeDiff).mockResolvedValue(firstDiffFixture())
    openUrl(`${EXCHANGE_URL}?tab=diff`)
    expect(await screen.findByText(/Primeira requisição/)).toBeInTheDocument()
  })
})
