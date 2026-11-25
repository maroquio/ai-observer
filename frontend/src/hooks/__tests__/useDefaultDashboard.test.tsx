import { beforeEach, describe, expect, it, vi } from 'vitest'
import { waitFor } from '@testing-library/react'
import { api } from '@/lib/api'
import { useDashboards, useDefaultDashboard } from '@/lib/queries'
import { renderHookWithProviders } from '@/test/queryWrapper'
import type { DashboardWithWidgets } from '@/types/dashboard'

vi.mock('@/lib/api', () => ({
  api: {
    getDashboards: vi.fn(),
    getDefaultDashboard: vi.fn(),
  },
}))

const DEFAULT = { id: 'd1', name: 'Default Dashboard', isDefault: true, createdAt: '', updatedAt: '', widgets: [] } as DashboardWithWidgets

describe('useDefaultDashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('reloads a dashboard list loaded before the server created the default dashboard', async () => {
    // A new database: the list is asked for before the default dashboard
    // request creates it
    vi.mocked(api.getDashboards)
      .mockResolvedValueOnce({ dashboards: [] })
      .mockResolvedValue({ dashboards: [DEFAULT] })
    vi.mocked(api.getDefaultDashboard).mockResolvedValue(DEFAULT)

    const { result } = renderHookWithProviders(() => ({ list: useDashboards(), dashboard: useDefaultDashboard() }))

    await waitFor(() => expect(result.current.list.data).toEqual([DEFAULT]))
    expect(api.getDashboards).toHaveBeenCalledTimes(2)
  })

  it('reloads a list still loading when the default dashboard arrives', async () => {
    let resolveList: (value: { dashboards: DashboardWithWidgets[] }) => void = () => {}
    vi.mocked(api.getDashboards)
      .mockReturnValueOnce(new Promise((resolve) => (resolveList = resolve)))
      .mockResolvedValue({ dashboards: [DEFAULT] })
    vi.mocked(api.getDefaultDashboard).mockResolvedValue(DEFAULT)

    const { result } = renderHookWithProviders(() => ({ list: useDashboards(), dashboard: useDefaultDashboard() }))
    await waitFor(() => expect(result.current.dashboard.isSuccess).toBe(true))
    resolveList({ dashboards: [] }) // the answer of a request sent before the creation

    await waitFor(() => expect(result.current.list.data).toEqual([DEFAULT]))
    expect(api.getDashboards).toHaveBeenCalledTimes(2)
  })

  it('does not create the default dashboard itself, so a deleted last dashboard stays deleted', async () => {
    vi.mocked(api.getDashboards).mockResolvedValue({ dashboards: [] })

    const { result } = renderHookWithProviders(() => useDashboards())

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual([])
    expect(api.getDefaultDashboard).not.toHaveBeenCalled()
    expect(api.getDashboards).toHaveBeenCalledTimes(1)
  })

  it('keeps an empty list when the default dashboard request in flight fails', async () => {
    let rejectDefault: (error: Error) => void = () => {}
    vi.mocked(api.getDashboards).mockResolvedValue({ dashboards: [] })
    vi.mocked(api.getDefaultDashboard).mockReturnValue(new Promise((_, reject) => (rejectDefault = reject)))

    const { result } = renderHookWithProviders(() => ({ list: useDashboards(), dashboard: useDefaultDashboard() }))
    await waitFor(() => expect(api.getDashboards).toHaveBeenCalled())
    rejectDefault(new Error('down'))

    await waitFor(() => expect(result.current.list.isSuccess).toBe(true))
    expect(result.current.list.data).toEqual([])
    expect(api.getDashboards).toHaveBeenCalledTimes(1)
  })

  it('leaves a list that has the default dashboard alone', async () => {
    let resolveDefault: (value: DashboardWithWidgets) => void = () => {}
    vi.mocked(api.getDashboards).mockResolvedValue({ dashboards: [DEFAULT] })
    vi.mocked(api.getDefaultDashboard).mockReturnValue(new Promise((resolve) => (resolveDefault = resolve)))

    const { result } = renderHookWithProviders(() => ({ list: useDashboards(), dashboard: useDefaultDashboard() }))
    await waitFor(() => expect(result.current.list.data).toEqual([DEFAULT]))
    resolveDefault(DEFAULT)
    await waitFor(() => expect(result.current.dashboard.isSuccess).toBe(true))

    expect(api.getDashboards).toHaveBeenCalledTimes(1)
  })
})
