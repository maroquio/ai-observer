import { Link, useParams } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/common/empty-state'
import { ExchangeInspector } from '@/components/http-inspector/ExchangeInspector'
import { useBackLink } from '@/hooks/useBackLink'
import { backLabel, runPath, sessionTrafficPath, HTTP_INSPECTOR_PATH } from '@/lib/httpInspector'

/**
 * The inspector of one exchange, at /sessions/:sessionId/http-exchanges/:exchangeId
 * (opened from a session's API traffic) or
 * /http-inspector/runs/:runId/exchanges/:exchangeId (from a run, also for
 * exchanges without a session). Both URLs work after a reload.
 */
export function HttpExchangePage() {
  const { sessionId, runId, exchangeId = '' } = useParams<{ sessionId?: string; runId?: string; exchangeId: string }>()
  const fallback = sessionId ? sessionTrafficPath(sessionId) : runId ? runPath(runId) : HTTP_INSPECTOR_PATH
  const back = useBackLink(fallback)

  const backButton = (
    <Button variant="ghost" size="sm" className="-ml-2 gap-2" asChild>
      <Link to={back.to}>
        <ArrowLeft className="h-4 w-4" aria-hidden="true" />
        {backLabel(back.to)}
      </Link>
    </Button>
  )

  if (!exchangeId) {
    return (
      <div className="space-y-6">
        {backButton}
        <EmptyState title="Informe o ID da troca" />
      </div>
    )
  }

  // Keyed by the exchange: tab states (body variant, event filter) belong to one exchange
  return <ExchangeInspector key={exchangeId} exchangeId={exchangeId} context={{ sessionId, runId }} backButton={backButton} />
}
