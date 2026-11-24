import { lazy, Suspense, type ReactNode } from 'react'
import { Routes, Route } from 'react-router-dom'
import { Skeleton } from '@/components/ui/skeleton'
import { Layout } from '@/components/layout/Layout'
import { RouteErrorBoundary } from '@/components/RouteErrorBoundary'
import { NotFoundPage } from '@/pages/NotFoundPage'

// Pages are loaded on demand so the initial bundle only contains the app shell
const Dashboard = lazy(() => import('@/pages/Dashboard').then((m) => ({ default: m.Dashboard })))
const TracesPage = lazy(() => import('@/pages/TracesPage').then((m) => ({ default: m.TracesPage })))
const TraceDetailPage = lazy(() => import('@/pages/TraceDetailPage').then((m) => ({ default: m.TraceDetailPage })))
const MetricsPage = lazy(() => import('@/pages/MetricsPage').then((m) => ({ default: m.MetricsPage })))
const LogsPage = lazy(() => import('@/pages/LogsPage').then((m) => ({ default: m.LogsPage })))
const SessionsPage = lazy(() => import('@/pages/SessionsPage').then((m) => ({ default: m.SessionsPage })))
const SessionTranscriptPage = lazy(() =>
  import('@/pages/SessionTranscriptPage').then((m) => ({ default: m.SessionTranscriptPage }))
)
const HttpInspectorPage = lazy(() => import('@/pages/HttpInspectorPage').then((m) => ({ default: m.HttpInspectorPage })))
const HttpRunPage = lazy(() => import('@/pages/HttpRunPage').then((m) => ({ default: m.HttpRunPage })))
const HttpExchangePage = lazy(() => import('@/pages/HttpExchangePage').then((m) => ({ default: m.HttpExchangePage })))
const DocsPage = lazy(() => import('@/pages/DocsPage').then((m) => ({ default: m.DocsPage })))
const StatusPage = lazy(() => import('@/pages/StatusPage').then((m) => ({ default: m.StatusPage })))

function PageFallback() {
  return (
    <div className="space-y-4 p-6" aria-busy="true">
      <Skeleton className="h-8 w-48" />
      <Skeleton className="h-64 w-full" />
    </div>
  )
}

function page(element: ReactNode) {
  return (
    <RouteErrorBoundary>
      <Suspense fallback={<PageFallback />}>{element}</Suspense>
    </RouteErrorBoundary>
  )
}

/**
 * The app's route table, inside the layout. Rendered by App within its
 * router; tests mount it in a MemoryRouter to open URLs as after a reload.
 * /http-inspector/runs/:runId is printed by `ai-observer run`: keep it.
 */
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
        <Route index element={page(<Dashboard />)} />
        <Route path="dashboard/:id" element={page(<Dashboard />)} />
        <Route path="traces" element={page(<TracesPage />)} />
        <Route path="traces/:kind/:id" element={page(<TraceDetailPage />)} />
        <Route path="metrics" element={page(<MetricsPage />)} />
        <Route path="logs" element={page(<LogsPage />)} />
        <Route path="sessions" element={page(<SessionsPage />)} />
        <Route path="sessions/:sessionId" element={page(<SessionTranscriptPage />)} />
        <Route path="sessions/:sessionId/http-exchanges/:exchangeId" element={page(<HttpExchangePage />)} />
        <Route path="http-inspector" element={page(<HttpInspectorPage />)} />
        <Route path="http-inspector/runs/:runId" element={page(<HttpRunPage />)} />
        <Route path="http-inspector/runs/:runId/exchanges/:exchangeId" element={page(<HttpExchangePage />)} />
        <Route path="docs" element={page(<DocsPage />)} />
        <Route path="status" element={page(<StatusPage />)} />
        <Route path="*" element={page(<NotFoundPage />)} />
      </Route>
    </Routes>
  )
}
