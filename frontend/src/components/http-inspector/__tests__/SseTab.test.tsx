import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SseTab } from '../inspector/SseTab'
import { api } from '@/lib/api'
import { renderWithProviders } from '@/test/queryWrapper'
import { eventsFixture, exchangeFixture, sseEvent } from '@/test/httpInspectorFixtures'

vi.mock('@/lib/api', () => ({
  api: { getHTTPExchangeEvents: vi.fn() },
}))

const TOTAL = 5_000
const PAGE = 1_000
const ALL_EVENTS = Array.from({ length: TOTAL }, (_, i) => sseEvent(i + 1))

// Pages of 1,000 events like the server, the cursor being the next offset
function serveEvents() {
  vi.mocked(api.getHTTPExchangeEvents).mockImplementation(async (_id, params = {}) => {
    const start = Number(params.cursor ?? 0)
    const end = Math.min(start + (params.limit ?? PAGE), TOTAL)
    return eventsFixture(ALL_EVENTS.slice(start, end), {
      total: TOTAL,
      hasMore: end < TOTAL,
      nextCursor: end < TOTAL ? String(end) : undefined,
      types: { content_block_delta: TOTAL },
    })
  })
}

function renderedRows(): number {
  return screen.queryAllByRole('button', { name: /^Evento \d+:/ }).length
}

describe('SseTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    serveEvents()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true, writable: true })
  })

  it('renders only the rows near the viewport of a stream of 5,000 events', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SseTab exchange={exchangeFixture({ sseEvents: TOTAL })} />)

    expect(await screen.findByText(`1,000 de 5,000 eventos carregados`)).toBeInTheDocument()
    for (let loaded = 2; loaded <= 5; loaded++) {
      await user.click(screen.getByRole('button', { name: 'Carregar mais eventos' }))
      expect(await screen.findByText(`${(loaded * PAGE).toLocaleString('en-US')} de 5,000 eventos carregados`)).toBeInTheDocument()
    }
    expect(screen.queryByRole('button', { name: 'Carregar mais eventos' })).not.toBeInTheDocument()
    expect(api.getHTTPExchangeEvents).toHaveBeenCalledTimes(5)

    const rows = renderedRows()
    expect(rows).toBeGreaterThan(0)
    expect(rows).toBeLessThan(100)
    expect(screen.getByRole('button', { name: 'Evento 1: content_block_delta' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: `Evento ${TOTAL}: content_block_delta` })).not.toBeInTheDocument()
    // The list keeps the full height, so the scroll bar matches the stream
    expect(screen.getByRole('list')).toHaveStyle({ height: `${TOTAL * 36}px` })

    // Scrolling to the end renders the last events instead of the first
    // (jsdom lays nothing out: the list moves up as the window scrolls)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => DOMRect.fromRect({ x: 0, y: -window.scrollY, width: 1024, height: 0 }))
    act(() => {
      Object.defineProperty(window, 'scrollY', { value: TOTAL * 36, configurable: true, writable: true })
      fireEvent.scroll(window)
    })
    await waitFor(() => expect(screen.getByRole('button', { name: `Evento ${TOTAL}: content_block_delta` })).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Evento 1: content_block_delta' })).not.toBeInTheDocument()
    expect(renderedRows()).toBeLessThan(100)
  })
})
