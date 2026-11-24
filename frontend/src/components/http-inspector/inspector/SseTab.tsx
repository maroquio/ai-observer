import { useMemo, useState } from 'react'
import { Radio } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { EmptyState } from '@/components/common/empty-state'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { QueryError } from '@/components/common/query-error'
import { errorText } from '@/lib/errors'
import { formatBytes, formatCount } from '@/lib/format'
import { formatMaybeMs, prettyJSON } from '@/lib/httpInspector'
import { useHTTPExchangeEvents } from '@/lib/queries'
import { cn } from '@/lib/utils'
import type { HTTPExchange, HTTPSSEEvent } from '@/types/httpInspector'
import { Fields, TextBlock } from '../primitives'
import { VirtualRows } from '../VirtualRows'

const ROW_HEIGHT = 36

/**
 * SSE Stream: the response's Server-Sent Events in order, with the time
 * since the request started; thousands of them render a screenful at a time
 */
export function SseTab({ exchange }: { exchange: HTTPExchange }) {
  const [type, setType] = useState('')
  const [selectedSeq, setSelectedSeq] = useState<number | null>(null)
  const streamed = exchange.stream || exchange.sseEvents > 0
  const query = useHTTPExchangeEvents(exchange.id, type, { enabled: streamed })
  const events = useMemo(() => query.data?.pages.flatMap((page) => page.events ?? []) ?? [], [query.data])
  const firstPage = query.data?.pages[0]
  const lastPage = query.data?.pages[query.data.pages.length - 1]
  const types = firstPage?.types ?? {}
  const allCount = Object.values(types).reduce((sum, n) => sum + n, 0)
  const selected = events.find((e) => e.seq === selectedSeq)

  if (!streamed) {
    return <EmptyState icon={Radio} title="Resposta sem streaming" description="Esta troca não recebeu eventos SSE: a resposta veio como um único corpo (veja a aba Response)." />
  }
  if (query.isPending) {
    return <ListSkeleton rows={8} rowClassName="h-8" label="Carregando eventos SSE" />
  }
  if (query.isError && !query.data) {
    return <QueryError error={query.error} title="Não foi possível carregar os eventos SSE" onRetry={() => void query.refetch()} retrying={query.isFetching} />
  }
  if (exchange.captureMode === 'metadata_only' && events.length === 0) {
    return <EmptyState icon={Radio} title="Eventos não armazenados" description="No modo metadata_only o conteúdo dos eventos não é gravado." />
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="w-full sm:w-72 *:w-full">
          <NativeSelect
            aria-label="Tipo de evento"
            value={type}
            onChange={(e) => {
              setType(e.target.value)
              setSelectedSeq(null)
            }}
          >
            <NativeSelectOption value="">Todos os tipos ({formatCount(allCount)})</NativeSelectOption>
            {Object.entries(types).map(([name, n]) => (
              <NativeSelectOption key={name} value={name}>
                {name} ({formatCount(n)})
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <span className="text-sm text-muted-foreground" role="status">
          {formatCount(events.length)} de {formatCount(lastPage?.total ?? events.length)} eventos carregados
        </span>
      </div>

      {events.length === 0 ? (
        <EmptyState icon={Radio} title="Nenhum evento deste tipo" />
      ) : (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,28rem)]">
          <div className="min-w-0">
            <div className="grid grid-cols-[4rem_5.5rem_minmax(0,1fr)_5rem] gap-2 border-b px-2 pb-1 text-xs font-medium text-muted-foreground" aria-hidden="true">
              <span>#</span>
              <span>tempo</span>
              <span>tipo</span>
              <span className="text-right">bytes</span>
            </div>
            <VirtualRows
              role="list"
              count={events.length}
              rowHeight={ROW_HEIGHT}
              rowKey={(index) => events[index].seq}
              renderRow={(index) => (
                <EventRow event={events[index]} selected={events[index].seq === selectedSeq} onSelect={() => setSelectedSeq(events[index].seq)} />
              )}
            />
            {query.hasNextPage && (
              <div className="mt-3 flex flex-col items-center gap-2">
                {query.isFetchNextPageError && <span className="text-sm text-error">{errorText(query.error, 'Falha ao carregar mais eventos')}</span>}
                <Button type="button" variant="outline" size="sm" onClick={() => void query.fetchNextPage()} disabled={query.isFetchingNextPage}>
                  {query.isFetchingNextPage ? 'Carregando…' : 'Carregar mais eventos'}
                </Button>
              </div>
            )}
          </div>
          <div className="lg:sticky lg:top-20 lg:self-start">
            {selected ? (
              <EventDetail event={selected} />
            ) : (
              <p className="rounded-md border p-4 text-sm text-muted-foreground">Selecione um evento para ver seu JSON.</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function EventRow({ event, selected, onSelect }: { event: HTTPSSEEvent; selected: boolean; onSelect: () => void }) {
  return (
    <div role="listitem" className="h-full">
      <button
        type="button"
        onClick={onSelect}
        aria-pressed={selected}
        aria-label={`Evento ${event.seq}: ${event.type}`}
        className={cn(
          'grid h-full w-full grid-cols-[4rem_5.5rem_minmax(0,1fr)_5rem] items-center gap-2 border-b px-2 text-left text-xs hover:bg-accent/50 focus-visible:bg-accent/50 focus-visible:outline-none',
          selected && 'bg-accent',
        )}
      >
        <span className="font-mono text-muted-foreground tabular-nums">{event.seq}</span>
        <span className="font-mono tabular-nums">+{formatMaybeMs(event.offsetMs) ?? '?'}</span>
        <span className="flex min-w-0 items-center gap-1">
          <span className="truncate font-mono">{event.type}</span>
          {event.index !== undefined && <span className="text-muted-foreground">[{event.index}]</span>}
          {event.deltaType && <span className="truncate text-muted-foreground">{event.deltaType}</span>}
          {event.parseStatus !== 'ok' && (
            <Badge variant="warning" className="shrink-0 px-1 py-0 text-[10px]">
              {event.parseStatus}
            </Badge>
          )}
        </span>
        <span className="text-right text-muted-foreground tabular-nums">{formatBytes(event.dataBytes)}</span>
      </button>
    </div>
  )
}

function EventDetail({ event }: { event: HTTPSSEEvent }) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <h3 className="font-mono text-sm font-semibold">
        #{event.seq} {event.type}
      </h3>
      <Fields
        className="sm:grid-cols-2 lg:grid-cols-2"
        items={[
          { label: 'Tempo desde o início', value: `+${formatMaybeMs(event.offsetMs) ?? '?'}` },
          { label: 'Campo event:', value: event.event ? <span className="font-mono">{event.event}</span> : '—' },
          { label: 'Interpretação', value: event.parseStatus },
          { label: 'Posição no corpo', value: `byte ${formatCount(event.byteOffset)} (${formatBytes(event.byteLen)})` },
        ]}
      />
      {event.note && <p className="text-sm text-muted-foreground">{event.note}</p>}
      {event.truncated && <Badge variant="warning">dados cortados pelo limite</Badge>}
      {event.data ? (
        <TextBlock text={prettyJSON(event.data).text} label={`Dados do evento ${event.seq}`} />
      ) : (
        <p className="text-sm text-muted-foreground">Sem dados armazenados.</p>
      )}
    </div>
  )
}
