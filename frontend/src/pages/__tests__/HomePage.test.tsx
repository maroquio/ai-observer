import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, screen, within } from '@testing-library/react'
import { api } from '@/lib/api'
import { HomePage } from '@/pages/HomePage'
import { renderWithProviders } from '@/test/queryWrapper'
import { httpInspectorStatusFixture, statusFixture } from '@/test/statusFixture'
import { useTelemetryStore } from '@/stores/telemetryStore'

vi.mock('@/lib/api', () => ({
  api: {
    getStatus: vi.fn(),
    getHTTPInspectorSessions: vi.fn(),
  },
}))

// The dashboard has its own tests; here only which page is shown matters
vi.mock('@/pages/Dashboard', () => ({ Dashboard: () => <p>Dashboard shown</p> }))

const PROXY_ON = httpInspectorStatusFixture({ enabled: true, proxyUrl: 'http://127.0.0.1:8787' })

describe('HomePage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.getHTTPInspectorSessions).mockResolvedValue({ sessions: [], total: 0, hasMore: false })
  })

  afterEach(() => {
    useTelemetryStore.setState({ metricsUpdateCount: 0, logsUpdateCount: 0, spansUpdateCount: 0 })
  })

  it('shows the data sources without telemetry: the proxy on, OTLP waiting, no session files', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture({ httpInspector: PROXY_ON, server: { otlpPort: 4400 } }))
    renderWithProviders(<HomePage />)

    expect(await screen.findByRole('heading', { name: 'Fontes de dados' })).toBeInTheDocument()
    expect(screen.queryByText('Dashboard shown')).not.toBeInTheDocument()

    const proxy = screen.getByRole('region', { name: 'Tráfego da API' })
    expect(within(proxy).getByText('Ligado')).toBeInTheDocument()
    expect(within(proxy).getByText('export ANTHROPIC_BASE_URL=http://127.0.0.1:8787')).toBeInTheDocument()
    expect(within(proxy).getByRole('link', { name: 'Abrir o HTTP Inspector' })).toHaveAttribute('href', '/http-inspector')

    const otlp = screen.getByRole('region', { name: 'Telemetria OTLP' })
    expect(within(otlp).getByText('Aguardando dados')).toBeInTheDocument()
    expect(within(otlp).getByText(/OTEL_EXPORTER_OTLP_ENDPOINT=http:\/\/localhost:4400/)).toBeInTheDocument()

    const files = screen.getByRole('region', { name: 'Arquivos de sessão' })
    expect(within(files).getByText('Desligado')).toBeInTheDocument()
    expect(within(files).getByText(/ai-observer import claude-code/)).toBeInTheDocument()
  })

  it('tells how to turn on what the server has off: the proxy, and OTLP in watch mode', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(
      statusFixture({ mode: 'watch', watchedTools: ['claude-code'], server: { otlpEnabled: false } }),
    )
    renderWithProviders(<HomePage />)

    const proxy = await screen.findByRole('region', { name: 'Tráfego da API' })
    expect(within(proxy).getByText('Desligado')).toBeInTheDocument()
    expect(within(proxy).getByText('ai-observer serve --http-proxy')).toBeInTheDocument()
    expect(within(proxy).queryByRole('link')).not.toBeInTheDocument()

    const otlp = screen.getByRole('region', { name: 'Telemetria OTLP' })
    expect(within(otlp).getByText('Desligada')).toBeInTheDocument()
    expect(within(otlp).getByText('ai-observer serve')).toBeInTheDocument()

    const files = screen.getByRole('region', { name: 'Arquivos de sessão' })
    expect(within(files).getByText('Acompanhando, sem registros')).toBeInTheDocument()
    expect(within(files).getByText(/Lendo claude-code/)).toBeInTheDocument()
  })

  it('links the stored captures while the proxy is off', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture())
    vi.mocked(api.getHTTPInspectorSessions).mockResolvedValue({ sessions: [], total: 2, hasMore: false })
    renderWithProviders(<HomePage />)

    const proxy = await screen.findByRole('region', { name: 'Tráfego da API' })
    expect(await within(proxy).findByText('Desligado, com capturas guardadas')).toBeInTheDocument()
    expect(within(proxy).getByRole('link', { name: 'Abrir o HTTP Inspector' })).toBeInTheDocument()
  })

  it('shows the dashboard when telemetry is stored', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture({ telemetry: { logCount: 1 } }))
    renderWithProviders(<HomePage />)

    expect(await screen.findByText('Dashboard shown')).toBeInTheDocument()
  })

  it('becomes the dashboard when the first telemetry arrives', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture({ httpInspector: PROXY_ON }))
    renderWithProviders(<HomePage />)
    await screen.findByRole('heading', { name: 'Fontes de dados' })

    act(() => useTelemetryStore.getState().addLogs({ count: 1, services: ['claude-code'], timestamps: [Date.now()] }))

    expect(screen.getByText('Dashboard shown')).toBeInTheDocument()
  })

  it('looks once more after a reload, when the cached counts predate the stored records', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      // The second answer is stale too: the first came from a fresh cache,
      // so only the second started the server's refresh
      vi.mocked(api.getStatus)
        .mockResolvedValueOnce(statusFixture({ httpInspector: PROXY_ON }))
        .mockResolvedValueOnce(statusFixture({ httpInspector: PROXY_ON }))
        .mockResolvedValue(statusFixture({ httpInspector: PROXY_ON, telemetry: { logCount: 2, metricCount: 4 } }))
      renderWithProviders(<HomePage />)
      await screen.findByRole('heading', { name: 'Fontes de dados' })

      await act(() => vi.advanceTimersByTimeAsync(6_000))
      expect(api.getStatus).toHaveBeenCalledTimes(2)
      expect(screen.getByRole('heading', { name: 'Fontes de dados' })).toBeInTheDocument()

      await act(() => vi.advanceTimersByTimeAsync(6_000))
      expect(await screen.findByText('Dashboard shown')).toBeInTheDocument()
      expect(api.getStatus).toHaveBeenCalledTimes(3)
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows the dashboard when the status fails, as before', async () => {
    vi.mocked(api.getStatus).mockRejectedValue(new Error('down'))
    renderWithProviders(<HomePage />)

    expect(await screen.findByText('Dashboard shown')).toBeInTheDocument()
  })
})
