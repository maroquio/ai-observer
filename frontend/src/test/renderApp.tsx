import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { AppRoutes } from '@/AppRoutes'
import { SidebarProvider } from '@/components/ui/sidebar'
import { createTestQueryClient } from './queryWrapper'

/**
 * Opens a URL in the app's real route table (layout and sidebar included)
 * with nothing cached, as after a reload. Tests mock `@/lib/api` with the
 * layout's calls (getProviders, getDashboards, getStatus) and the page's.
 */
export function renderApp(url: string) {
  const router = createMemoryRouter(
    [
      {
        path: '*',
        element: (
          <SidebarProvider>
            <AppRoutes />
          </SidebarProvider>
        ),
      },
    ],
    { initialEntries: [url] },
  )
  const queryClient = createTestQueryClient()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, queryClient, ...result }
}

/**
 * Imports the lazily loaded pages the HTTP Inspector tests open, so a
 * test's first render does not wait for a cold import (call in beforeAll)
 */
export async function preloadHttpInspectorPages() {
  await Promise.all([
    import('@/pages/HttpInspectorPage'),
    import('@/pages/HttpRunPage'),
    import('@/pages/HttpSessionPage'),
    import('@/pages/HttpExchangePage'),
    import('@/pages/SessionTranscriptPage'),
  ])
}
