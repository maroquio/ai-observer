import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AppSidebar } from '../Sidebar'
import { SidebarProvider, useSidebar } from '@/components/ui/sidebar'
import { api } from '@/lib/api'
import { renderWithProviders } from '@/test/queryWrapper'
import { httpInspectorStatusFixture, statusFixture } from '@/test/statusFixture'
import { useTelemetryStore } from '@/stores/telemetryStore'
import type { StatusResponse } from '@/types/status'

vi.mock('@/lib/api', () => ({
  api: {
    getDashboards: vi.fn(),
    getStatus: vi.fn(),
    getHTTPInspectorSessions: vi.fn(),
  },
}))

function OpenMobileSidebar() {
  const { setOpenMobile } = useSidebar()
  return <button onClick={() => setOpenMobile(true)}>Open menu</button>
}

function renderSidebar(route: string) {
  return renderWithProviders(
    <SidebarProvider>
      <OpenMobileSidebar />
      <AppSidebar />
    </SidebarProvider>,
    { route }
  )
}

const originalMatchMedia = window.matchMedia

function setMobile(mobile: boolean) {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: mobile,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }))
}

describe('AppSidebar', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.getDashboards).mockResolvedValue({
      dashboards: [
        { id: 'd1', name: 'Default Dashboard', isDefault: true, createdAt: '', updatedAt: '' },
        { id: 'd2', name: 'Costs', isDefault: false, createdAt: '', updatedAt: '' },
      ],
    })
    vi.mocked(api.getStatus).mockResolvedValue({ version: 'v0.7.1', mode: 'serve' } as StatusResponse)
  })

  afterEach(() => {
    window.matchMedia = originalMatchMedia
    useTelemetryStore.setState({ metricsUpdateCount: 0, logsUpdateCount: 0, spansUpdateCount: 0 })
  })

  it('highlights the current page, also below it, and marks it for assistive technology', async () => {
    renderSidebar('/traces/otel_trace/abc')

    const traces = screen.getByRole('link', { name: 'Traces' })
    expect(traces).toHaveAttribute('data-active', 'true')
    expect(traces).toHaveAttribute('aria-current', 'page')
    const logs = screen.getByRole('link', { name: 'Logs' })
    expect(logs).toHaveAttribute('data-active', 'false')
    expect(logs).not.toHaveAttribute('aria-current')

    // No dashboard is current on another page
    await screen.findByRole('link', { name: /Costs/ })
    expect(screen.getByRole('link', { name: /Default Dashboard/ })).toHaveAttribute('data-active', 'false')
  })

  it('highlights the shown dashboard', async () => {
    renderSidebar('/dashboard/d2')

    const costs = await screen.findByRole('link', { name: /Costs/ })
    expect(costs).toHaveAttribute('data-active', 'true')
    expect(costs).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: /Default Dashboard/ })).toHaveAttribute('data-active', 'false')
  })

  it('links the status page and shows the server version', async () => {
    renderSidebar('/status')

    expect(screen.getByRole('link', { name: 'Status' })).toHaveAttribute('data-active', 'true')
    expect(screen.getByRole('link', { name: 'Documentation' })).toHaveAttribute('href', '/docs')
    expect(await screen.findByRole('link', { name: 'AI Observer v0.7.1' })).toHaveAttribute('href', '/status')
  })

  it('closes on phones when a page is chosen', async () => {
    setMobile(true)
    const user = userEvent.setup()
    renderSidebar('/')

    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('link', { name: 'Metrics' }))

    await waitFor(() => expect(screen.queryByRole('link', { name: 'Metrics' })).not.toBeInTheDocument())
  })
})

describe('AppSidebar data sources', () => {
  const PROXY_ON = httpInspectorStatusFixture({ enabled: true, proxyUrl: 'http://127.0.0.1:8787' })

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.getDashboards).mockResolvedValue({
      dashboards: [{ id: 'd1', name: 'Default Dashboard', isDefault: true, createdAt: '', updatedAt: '' }],
    })
    vi.mocked(api.getHTTPInspectorSessions).mockResolvedValue({ sessions: [], total: 0, hasMore: false })
  })

  afterEach(() => {
    useTelemetryStore.setState({ metricsUpdateCount: 0, logsUpdateCount: 0, spansUpdateCount: 0 })
  })

  it('shows only the HTTP Inspector and a home link when only the proxy has data', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture({ httpInspector: PROXY_ON }))
    renderSidebar('/http-inspector')

    expect(await screen.findByRole('link', { name: 'Home' })).toHaveAttribute('href', '/')
    expect(screen.getByRole('link', { name: 'HTTP Inspector' })).toBeInTheDocument()
    for (const name of ['Metrics', 'Logs', 'Traces', 'Sessions']) {
      expect(screen.queryByRole('link', { name })).not.toBeInTheDocument()
    }
    expect(screen.queryByText('Dashboards')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Status' })).toBeInTheDocument()
  })

  it("shows the pages of Claude Code's telemetry: no Traces without spans", async () => {
    vi.mocked(api.getStatus).mockResolvedValue(
      statusFixture({ telemetry: { metricCount: 10, logCount: 5 }, httpInspector: PROXY_ON }),
    )
    renderSidebar('/metrics')

    await waitFor(() => expect(screen.queryByRole('link', { name: 'Traces' })).not.toBeInTheDocument())
    for (const name of ['Metrics', 'Logs', 'Sessions', 'HTTP Inspector']) {
      expect(screen.getByRole('link', { name })).toBeInTheDocument()
    }
    expect(screen.getByText('Dashboards')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Home' })).not.toBeInTheDocument()
  })

  it('keeps the entry of the page shown, opened by a link, without data', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture({ telemetry: { metricCount: 10 } }))
    renderSidebar('/traces/otel_trace/abc')

    await waitFor(() => expect(screen.queryByRole('link', { name: 'Logs' })).not.toBeInTheDocument())
    expect(screen.getByRole('link', { name: 'Traces' })).toHaveAttribute('aria-current', 'page')
  })

  it('hides the HTTP Inspector with the proxy off unless captures are stored', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture({ telemetry: { metricCount: 1 } }))
    const { unmount } = renderSidebar('/metrics')
    await waitFor(() => expect(screen.queryByRole('link', { name: 'HTTP Inspector' })).not.toBeInTheDocument())
    expect(api.getHTTPInspectorSessions).toHaveBeenCalledWith({ limit: 1 }, expect.anything())
    unmount()

    vi.mocked(api.getHTTPInspectorSessions).mockResolvedValue({
      sessions: [{ sessionId: 's1' } as never], total: 1, hasMore: false,
    })
    renderSidebar('/metrics')
    await waitFor(() => expect(api.getHTTPInspectorSessions).toHaveBeenCalledTimes(2))
    expect(screen.getByRole('link', { name: 'HTTP Inspector' })).toBeInTheDocument()
  })

  it('shows a page as soon as its first records are announced', async () => {
    vi.mocked(api.getStatus).mockResolvedValue(statusFixture({ httpInspector: PROXY_ON }))
    renderSidebar('/http-inspector')
    await screen.findByRole('link', { name: 'Home' })
    expect(screen.queryByRole('link', { name: 'Metrics' })).not.toBeInTheDocument()

    act(() => useTelemetryStore.getState().addMetrics({ count: 3, services: ['claude-code'], timestamps: [] }))

    expect(screen.getByRole('link', { name: 'Metrics' })).toBeInTheDocument()
    expect(screen.getByText('Dashboards')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Home' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Traces' })).not.toBeInTheDocument()
  })

  it('shows every page when the status fails', async () => {
    vi.mocked(api.getStatus).mockRejectedValue(new Error('down'))
    const { queryClient } = renderSidebar('/http-inspector')

    await waitFor(() => expect(queryClient.getQueryState(['status'])?.status).toBe('error'))
    for (const name of ['Metrics', 'Logs', 'Traces', 'Sessions', 'HTTP Inspector']) {
      expect(screen.getByRole('link', { name })).toBeInTheDocument()
    }
    expect(screen.getByText('Dashboards')).toBeInTheDocument()
  })
})
