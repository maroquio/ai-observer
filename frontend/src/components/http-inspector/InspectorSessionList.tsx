import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { ChevronRight } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ListSkeleton, Refreshing } from '@/components/common/list-skeleton'
import { QueryError } from '@/components/common/query-error'
import { errorText } from '@/lib/errors'
import { formatCost, formatCount } from '@/lib/format'
import { inspectorSessionPath, shortSessionId } from '@/lib/httpInspector'
import { useHTTPInspectorSessions } from '@/lib/queries'
import { cn, formatTimestamp } from '@/lib/utils'
import type { HTTPInspectorSession } from '@/types/httpInspector'
import { CaptureHelp } from './CaptureHelp'

/**
 * The sessions with captured traffic, the most recently active first: each
 * opens its turns. Exchanges without a session ID are one row of their own.
 */
export function InspectorSessionList() {
  const sessions = useHTTPInspectorSessions()
  const rows = useMemo(() => sessions.data?.pages.flatMap((p) => p.sessions ?? []) ?? [], [sessions.data])
  const total = sessions.data?.pages[sessions.data.pages.length - 1]?.total ?? rows.length

  if (sessions.isPending) {
    return <ListSkeleton rows={4} rowClassName="h-20" label="Carregando sessões" />
  }
  if (sessions.isError && !sessions.data) {
    return (
      <QueryError error={sessions.error} title="Não foi possível carregar as sessões" onRetry={() => void sessions.refetch()} retrying={sessions.isFetching} />
    )
  }
  if (rows.length === 0) {
    return <CaptureHelp title="Nenhuma sessão capturada" intro="Ainda não há tráfego capturado. Inicie o servidor com o proxy e rode o Claude Code pelo launcher:" />
  }
  return (
    <Refreshing active={sessions.isFetching && !sessions.isFetchingNextPage}>
      <ul className="space-y-2" aria-label="Sessões capturadas">
        {rows.map((session) => (
          <SessionRow key={session.sessionId || '-'} session={session} />
        ))}
      </ul>
      <div className="mt-4 flex flex-col items-center gap-2 text-sm text-muted-foreground">
        <span>
          {formatCount(rows.length)} de {formatCount(total)} {total === 1 ? 'sessão' : 'sessões'}
        </span>
        {sessions.isFetchNextPageError && <span className="text-error">{errorText(sessions.error, 'Falha ao carregar mais sessões')}</span>}
        {sessions.hasNextPage && (
          <Button type="button" variant="outline" size="sm" onClick={() => void sessions.fetchNextPage()} disabled={sessions.isFetchingNextPage}>
            {sessions.isFetchingNextPage ? 'Carregando…' : 'Carregar mais sessões'}
          </Button>
        )}
      </div>
    </Refreshing>
  )
}

function SessionRow({ session }: { session: HTTPInspectorSession }) {
  const sessionless = session.sessionId === ''
  const title = sessionless ? 'Sem sessão' : session.title || `Sessão ${shortSessionId(session.sessionId)}`
  return (
    <li className="relative flex flex-col gap-2 rounded-lg border p-4 transition-colors hover:bg-accent/50 focus-within:ring-2 focus-within:ring-ring/50 sm:flex-row sm:items-center sm:gap-4">
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Link
            to={inspectorSessionPath(session.sessionId)}
            className={cn('min-w-0 truncate font-medium outline-none after:absolute after:inset-0 after:content-[\'\']', sessionless && 'text-warning')}
            title={sessionless ? undefined : session.title || session.sessionId}
          >
            {title}
          </Link>
          {session.inProgress > 0 && <Badge variant="success">Em andamento</Badge>}
          {session.errors > 0 && <Badge variant="warning">{formatCount(session.errors)} {session.errors === 1 ? 'erro' : 'erros'}</Badge>}
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <span>{formatTimestamp(session.lastAt)}</span>
          {sessionless ? (
            <span>Requisições que chegaram sem ID de sessão (checagens de conectividade, outros endpoints)</span>
          ) : (
            <>
              <span className="font-mono" title={session.sessionId}>
                {shortSessionId(session.sessionId)}
              </span>
              <span>
                {formatCount(session.turns)} {session.turns === 1 ? 'turno' : 'turnos'}
              </span>
              <span>
                {formatCount(session.inference)} {session.inference === 1 ? 'inferência' : 'inferências'}
              </span>
              {session.models.length > 0 && <span>{session.models.join(', ')}</span>}
            </>
          )}
        </div>
      </div>
      <dl className="grid shrink-0 grid-cols-3 gap-4 text-sm sm:text-right">
        <div className="flex flex-col-reverse">
          <dt className="text-xs text-muted-foreground">{session.exchanges === 1 ? 'troca' : 'trocas'}</dt>
          <dd className="font-medium tabular-nums">{formatCount(session.exchanges)}</dd>
        </div>
        <div className="flex flex-col-reverse">
          <dt className="text-xs text-muted-foreground">entrada / saída</dt>
          <dd className="font-medium tabular-nums">
            {formatCount(session.totalInputTokens)} / {formatCount(session.outputTokens)}
          </dd>
        </div>
        <div className="flex flex-col-reverse">
          <dt className="text-xs text-muted-foreground">custo estimado</dt>
          <dd className="font-medium tabular-nums">{session.costStatus === 'unavailable' ? '—' : formatCost(session.costUsd)}</dd>
        </div>
      </dl>
      <ChevronRight className="hidden h-5 w-5 shrink-0 text-muted-foreground sm:block" aria-hidden="true" />
    </li>
  )
}
