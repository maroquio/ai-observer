import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { api } from '@/lib/api'
import { httpError } from '@/lib/errors'
import { preloadHttpInspectorPages, renderApp } from '@/test/renderApp'
import { httpInspectorStatusFixture, statusFixture } from '@/test/statusFixture'
import { RUN_ID, SESSION_ID, exchangeFixture, mixedExchanges, runFixture, summaryFixture, toolsFixture } from '@/test/httpInspectorFixtures'
import type { HTTPCaptureRun, HTTPExchange } from '@/types/httpInspector'

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
  },
  httpRunExportUrl: (runId: string) => `/api/http-inspector/runs/${encodeURIComponent(runId)}/export`,
}))

function serveRun(run: HTTPCaptureRun, exchanges: HTTPExchange[] = mixedExchanges()) {
  vi.mocked(api.getHTTPCaptureRun).mockResolvedValue({ run, summary: summaryFixture() })
  vi.mocked(api.getHTTPRunExchanges).mockResolvedValue({ exchanges, total: exchanges.length, hasMore: false })
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
  serveRun(runFixture())
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('HttpInspectorPage', () => {
  it('explains how to capture when the proxy is off and nothing was captured', async () => {
    renderApp('/http-inspector')
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
    const { router } = renderApp('/http-inspector')

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
