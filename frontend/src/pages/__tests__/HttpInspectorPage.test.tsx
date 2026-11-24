import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { api } from '@/lib/api'
import { httpError } from '@/lib/errors'
import { preloadHttpInspectorPages, renderApp } from '@/test/renderApp'
import { httpInspectorStatusFixture, statusFixture } from '@/test/statusFixture'
import { RUN_ID, SESSION_ID, exchangeFixture, mixedExchanges, runFixture, summaryFixture, toolsFixture } from '@/test/httpInspectorFixtures'
import type { HTTPCaptureRun, HTTPExchange, HTTPInspectorSession, HTTPSessionTurnsResponse, HTTPTurn } from '@/types/httpInspector'

vi.mock('@/lib/api', () => ({
  api: {
    getProviders: vi.fn(),
    getDashboards: vi.fn(),
    getStatus: vi.fn(),
    getHTTPInspectorStatus: vi.fn(),
    getHTTPCaptureRuns: vi.fn(),
    getHTTPCaptureRun: vi.fn(),
    updateHTTPCaptureRun: vi.fn(),
    deleteHTTPCaptureRun: vi.fn(),
    getHTTPRunExchanges: vi.fn(),
    getHTTPRunTools: vi.fn(),
    getHTTPInspectorSessions: vi.fn(),
    getHTTPSessionTurns: vi.fn(),
  },
  httpRunExportUrl: (runId: string) => `/api/http-inspector/runs/${encodeURIComponent(runId)}/export`,
}))

function serveRun(run: HTTPCaptureRun, exchanges: HTTPExchange[] = mixedExchanges()) {
  vi.mocked(api.getHTTPCaptureRun).mockResolvedValue({ run, summary: summaryFixture() })
  vi.mocked(api.getHTTPRunExchanges).mockResolvedValue({ exchanges, total: exchanges.length, hasMore: false })
}

function sessionFixture(overrides: Partial<HTTPInspectorSession> = {}): HTTPInspectorSession {
  return {
    sessionId: SESSION_ID,
    title: 'Revise src/auth.ts',
    firstAt: '2026-11-24T18:00:00Z',
    lastAt: '2026-11-24T18:05:00Z',
    exchanges: 7,
    inference: 4,
    turns: 2,
    errors: 1,
    inProgress: 0,
    totalInputTokens: 12345,
    outputTokens: 678,
    costUsd: 0.12,
    costStatus: 'complete',
    models: ['claude-opus-5-5'],
    runs: 1,
    ...overrides,
  }
}

function turnFixture(index: number, exchanges: HTTPExchange[], overrides: Partial<HTTPTurn> = {}): HTTPTurn {
  const tokens = { input: 10, output: 5, cacheRead: 100, cacheWrite: 20, cacheWrite5m: 20, cacheWrite1h: 0, cacheWriteOther: 0, totalInput: 130, totalProcessed: 135, exchanges: 1 }
  return {
    index,
    startedAt: exchanges[0]?.startedAt ?? '2026-11-24T18:00:00Z',
    endedAt: '2026-11-24T18:01:00Z',
    durationMs: 60_000,
    exchanges,
    inference: exchanges.filter((e) => e.kind === 'model_inference').length,
    auxiliary: exchanges.filter((e) => e.kind !== 'model_inference').length,
    errors: 0,
    toolUses: [],
    tokens,
    partialTokens: { ...tokens, input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cacheWrite5m: 0, totalInput: 0, totalProcessed: 0, exchanges: 0 },
    costUsd: 0.05,
    costStatus: 'complete',
    models: ['claude-opus-5-5'],
    ...overrides,
  }
}

function turnsFixture(overrides: Partial<HTTPSessionTurnsResponse> = {}): HTTPSessionTurnsResponse {
  const ex = mixedExchanges()
  return {
    sessionId: SESSION_ID,
    summary: summaryFixture(),
    runs: [runFixture()],
    preamble: turnFixture(0, [exchangeFixture({ id: 'ex-0', seq: 0, kind: 'quota_probe', status: 429, stream: false, sseEvents: 0, toolUses: [] })]),
    turns: [
      turnFixture(1, [ex[0], ex[1]], { prompt: 'Revise <b>src/auth.ts</b>', toolUses: ['Read', 'Read', 'Grep'] }),
      turnFixture(2, [ex[2], ex[3]], { prompt: undefined, errors: 2 }),
    ],
    truncated: false,
    note: 'Turnos deduzidos das requisições.',
    ...overrides,
  }
}

async function timelineRow(seq: number) {
  const link = await screen.findByRole('link', { name: new RegExp(`^Troca #${seq}:`) })
  return link.closest('[role="row"]') as HTMLElement
}

beforeAll(() => preloadHttpInspectorPages(), 20_000)

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  vi.mocked(api.getProviders).mockResolvedValue({ providers: [] })
  vi.mocked(api.getDashboards).mockResolvedValue({ dashboards: [] })
  vi.mocked(api.getStatus).mockResolvedValue(statusFixture())
  vi.mocked(api.getHTTPInspectorStatus).mockResolvedValue(httpInspectorStatusFixture())
  vi.mocked(api.getHTTPCaptureRuns).mockResolvedValue({ runs: [], total: 0, hasMore: false })
  vi.mocked(api.getHTTPRunTools).mockResolvedValue(toolsFixture())
  vi.mocked(api.getHTTPInspectorSessions).mockResolvedValue({ sessions: [], total: 0, hasMore: false })
  vi.mocked(api.getHTTPSessionTurns).mockResolvedValue(turnsFixture())
  serveRun(runFixture())
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('HttpInspectorPage', () => {
  it('explains how to capture when the proxy is off and nothing was captured', async () => {
    renderApp('/http-inspector?tab=runs')
    expect(await screen.findByRole('heading', { name: 'HTTP Inspector' })).toBeInTheDocument()
    expect(await screen.findByText('Desligado')).toBeInTheDocument()
    expect(await screen.findByText('Nenhuma execução de captura')).toBeInTheDocument()
    expect(screen.getByLabelText('Comandos para capturar o tráfego')).toHaveTextContent('ai-observer run -- claude')
    expect(screen.queryByRole('list', { name: 'Execuções de captura' })).not.toBeInTheDocument()
    // The sidebar links the page
    expect(screen.getByRole('link', { name: 'HTTP Inspector' })).toHaveAttribute('href', '/http-inspector')
  })

  it('lists the capture runs with the proxy status, filtered in the URL', async () => {
    const user = userEvent.setup()
    vi.mocked(api.getHTTPInspectorStatus).mockResolvedValue(
      httpInspectorStatusFixture({ enabled: true, proxyUrl: 'http://127.0.0.1:8787', warnings: ['Fila de captura perto do limite'] }),
    )
    vi.mocked(api.getHTTPCaptureRuns).mockResolvedValue({
      runs: [runFixture(), runFixture({ id: 'run-2', name: 'Manual', mode: 'manual', state: 'active', sessionIds: [], unattributed: 3 })],
      total: 2,
      hasMore: false,
    })
    const { router } = renderApp('/http-inspector?tab=runs')

    expect(await screen.findByText('Ativo')).toBeInTheDocument()
    expect(screen.getByText('http://127.0.0.1:8787')).toBeInTheDocument()
    expect(within(screen.getByRole('list', { name: 'Avisos do proxy' })).getByText('Fila de captura perto do limite')).toBeInTheDocument()

    const runs = await screen.findByRole('list', { name: 'Execuções de captura' })
    expect(within(runs).getByRole('link', { name: 'Aula 3: refatoração' })).toHaveAttribute('href', `/http-inspector/runs/${RUN_ID}`)
    expect(within(runs).getByText('3 sem sessão')).toBeInTheDocument()

    await user.selectOptions(screen.getByRole('combobox', { name: 'Modo da execução' }), 'manual')
    expect(new URLSearchParams(router.state.location.search).get('mode')).toBe('manual')
    await waitFor(() => expect(api.getHTTPCaptureRuns).toHaveBeenLastCalledWith(expect.objectContaining({ mode: 'manual' }), expect.anything()))
  })
})

describe('HttpRunPage', () => {
  it('opens a run from the URL the launcher prints', async () => {
    renderApp(`/http-inspector/runs/${RUN_ID}`)
    expect(await screen.findByRole('heading', { name: 'Aula 3: refatoração' })).toBeInTheDocument()
    expect(api.getHTTPCaptureRun).toHaveBeenCalledWith(RUN_ID, expect.anything())
    expect(screen.getByText('claude')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: SESSION_ID })).toHaveAttribute('href', `/sessions/${SESSION_ID}?view=traffic`)
    expect(screen.getByRole('link', { name: /Exportar zip/ })).toHaveAttribute('href', `/api/http-inspector/runs/${RUN_ID}/export`)
    // Exchanges open under the run
    expect(await screen.findByRole('link', { name: 'Troca #1: POST /v1/messages?beta=true' })).toHaveAttribute(
      'href',
      `/http-inspector/runs/${RUN_ID}/exchanges/ex-1`,
    )
    expect(screen.getByRole('link', { name: 'Voltar ao HTTP Inspector' })).toHaveAttribute('href', '/http-inspector')
  })

  it('marks exchanges no session ID links: not correlated, or only to the launcher run', async () => {
    serveRun(runFixture({ mode: 'manual' }), [
      exchangeFixture(),
      exchangeFixture({ id: 'ex-2', seq: 2, sessionId: undefined, sessionEvidence: undefined }),
    ])
    renderApp(`/http-inspector/runs/${RUN_ID}`)
    expect(within(await timelineRow(2)).getByText('não correlacionado')).toBeInTheDocument()
    expect(within(await timelineRow(1)).queryByText('não correlacionado')).not.toBeInTheDocument()
  })

  it('marks launcher exchanges without a session ID as linked to the run only', async () => {
    serveRun(runFixture({ mode: 'launcher' }), [exchangeFixture({ sessionId: undefined, sessionEvidence: undefined })])
    renderApp(`/http-inspector/runs/${RUN_ID}`)
    expect(within(await timelineRow(1)).getByText('só launcher')).toBeInTheDocument()
  })

  it('renames a run', async () => {
    const user = userEvent.setup()
    vi.mocked(api.updateHTTPCaptureRun).mockResolvedValue(runFixture({ name: 'Aula 4' }))
    renderApp(`/http-inspector/runs/${RUN_ID}`)
    await user.click(await screen.findByRole('button', { name: 'Renomear execução' }))
    const input = screen.getByLabelText('Nome da execução')
    await user.clear(input)
    await user.type(input, 'Aula 4')
    serveRun(runFixture({ name: 'Aula 4' }))
    await user.click(screen.getByRole('button', { name: 'Salvar' }))

    expect(api.updateHTTPCaptureRun).toHaveBeenCalledWith(RUN_ID, { name: 'Aula 4' })
    expect(await screen.findByRole('heading', { name: 'Aula 4' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Nome da execução')).not.toBeInTheDocument()
  })

  it('does not delete an active run', async () => {
    serveRun(runFixture({ state: 'active', endedAt: undefined }))
    renderApp(`/http-inspector/runs/${RUN_ID}`)
    expect(await screen.findByRole('button', { name: 'Excluir' })).toBeDisabled()
  })

  it('explains a refused deletion, then deletes and returns to the list', async () => {
    const user = userEvent.setup()
    vi.mocked(api.deleteHTTPCaptureRun).mockRejectedValueOnce(httpError(409, 'run is active')).mockResolvedValueOnce(undefined)
    const { router } = renderApp(`/http-inspector/runs/${RUN_ID}`)

    await user.click(await screen.findByRole('button', { name: 'Excluir' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Excluir execução de captura' })
    await user.click(within(dialog).getByRole('button', { name: 'Excluir' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('A execução está ativa: termine-a antes de excluir.')

    await user.click(within(dialog).getByRole('button', { name: 'Excluir' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/http-inspector'))
    expect(api.deleteHTTPCaptureRun).toHaveBeenCalledTimes(2)
    expect(api.deleteHTTPCaptureRun).toHaveBeenLastCalledWith(RUN_ID)
  })

  it('says when a run does not exist', async () => {
    vi.mocked(api.getHTTPCaptureRun).mockRejectedValue(httpError(404, 'run not found'))
    renderApp('/http-inspector/runs/nope')
    expect(await screen.findByText('Execução não encontrada')).toBeInTheDocument()
  })
})

describe('HttpInspectorPage sessions', () => {
  it('lists the captured sessions by default, with the exchanges without a session apart', async () => {
    const user = userEvent.setup()
    vi.mocked(api.getHTTPInspectorSessions).mockResolvedValue({
      sessions: [sessionFixture(), sessionFixture({ sessionId: '', title: undefined, turns: 0, inference: 0, exchanges: 5, models: [], costStatus: 'unavailable' })],
      total: 2,
      hasMore: false,
    })
    const { router } = renderApp('/http-inspector')

    const list = await screen.findByRole('list', { name: 'Sessões capturadas' })
    expect(within(list).getByRole('link', { name: 'Revise src/auth.ts' })).toHaveAttribute('href', `/http-inspector/sessions/${SESSION_ID}`)
    expect(within(list).getByRole('link', { name: 'Sem sessão' })).toHaveAttribute('href', '/http-inspector/sessions/none')
    expect(within(list).getByText('2 turnos')).toBeInTheDocument()
    expect(api.getHTTPCaptureRuns).not.toHaveBeenCalled()

    await user.click(screen.getByRole('tab', { name: 'Execuções de captura' }))
    expect(new URLSearchParams(router.state.location.search).get('tab')).toBe('runs')
    await waitFor(() => expect(api.getHTTPCaptureRuns).toHaveBeenCalled())
  })
})

describe('HttpSessionPage', () => {
  it('shows a session as turns, the prompt as text, auxiliary calls on request', async () => {
    const user = userEvent.setup()
    renderApp(`/http-inspector/sessions/${SESSION_ID}`)

    expect(await screen.findByRole('heading', { name: 'Sessão 0a1b2c3d…' })).toBeInTheDocument()
    expect(api.getHTTPSessionTurns).toHaveBeenCalledWith(SESSION_ID, expect.anything())
    const first = screen.getByRole('article', { name: 'Turno 1' })
    // The prompt is text, never markup
    expect(within(first).getByLabelText('Prompt do turno')).toHaveTextContent('Revise <b>src/auth.ts</b>')
    expect(first.querySelector('b')).toBeNull()
    expect(within(first).getByLabelText('Ferramentas solicitadas')).toHaveTextContent('Read ×2')
    expect(within(screen.getByRole('article', { name: 'Turno 2' })).getByText(/Prompt não registrado/)).toBeInTheDocument()
    expect(screen.getByRole('article', { name: 'Antes do primeiro prompt' })).toBeInTheDocument()

    // Closed until opened; count_tokens (ex-2) hidden until asked for
    expect(screen.queryByRole('link', { name: /^Troca #1:/ })).not.toBeInTheDocument()
    await user.click(within(first).getByRole('button', { name: /Turno 1/ }))
    expect(await screen.findByRole('link', { name: /^Troca #1:/ })).toHaveAttribute('href', `/sessions/${SESSION_ID}/http-exchanges/ex-1`)
    expect(screen.queryByRole('link', { name: /^Troca #2:/ })).not.toBeInTheDocument()
    expect(within(first).getByText(/1 chamada auxiliar oculta/)).toBeInTheDocument()
    await user.click(screen.getByRole('switch', { name: 'Mostrar chamadas auxiliares' }))
    expect(await screen.findByRole('link', { name: /^Troca #2:/ })).toBeInTheDocument()
  })

  it('opens the exchanges without a session, linked through their runs', async () => {
    vi.mocked(api.getHTTPSessionTurns).mockResolvedValue(
      turnsFixture({ sessionId: '', turns: [], comparison: undefined, preamble: turnFixture(0, [exchangeFixture({ id: 'ex-9', seq: 9, kind: 'other_api', sessionId: undefined })]) }),
    )
    renderApp('/http-inspector/sessions/none')
    expect(await screen.findByRole('heading', { name: 'Sem sessão' })).toBeInTheDocument()
    expect(api.getHTTPSessionTurns).toHaveBeenCalledWith('', expect.anything())
    expect(await screen.findByRole('link', { name: /^Troca #9:/ })).toHaveAttribute('href', `/http-inspector/runs/${RUN_ID}/exchanges/ex-9`)
  })

  it('says when nothing was captured for the session', async () => {
    vi.mocked(api.getHTTPSessionTurns).mockRejectedValue(httpError(404, 'no captured traffic for this session'))
    renderApp('/http-inspector/sessions/nope')
    expect(await screen.findByText('Nenhum tráfego capturado')).toBeInTheDocument()
  })
})
