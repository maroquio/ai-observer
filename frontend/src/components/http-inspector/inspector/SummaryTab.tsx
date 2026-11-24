import { useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { formatBytes, formatCount } from '@/lib/format'
import {
  CAPTURE_MODE_LABELS,
  CERTAINTY_LABELS,
  LINK_QUALITY_LABELS,
  USAGE_QUALITY_LABELS,
  endpointLabel,
  expectsUsage,
  formatClock,
  formatMaybeMs,
  formatOffset,
  lifecycleLabel,
  linkQuality,
  observationOrigin,
  runPath,
  sessionTrafficPath,
} from '@/lib/httpInspector'
import { useHTTPExchangeEventTypes } from '@/lib/queries'
import { formatTimestamp } from '@/lib/utils'
import type { HTTPExchangeDetail, WireObservation } from '@/types/httpInspector'
import { Absent, Fields, HttpStatusBadge, OriginBadge, Section, StateBadge } from '../primitives'

/**
 * Resumo: what was sent, what was received and what happened, each stated
 * from what the proxy observed, with the evidence behind it
 */
export function SummaryTab({ detail }: { detail: HTTPExchangeDetail }) {
  const exchange = detail.exchange
  const streamed = exchange.stream && exchange.sseEvents > 0
  const eventTypes = useHTTPExchangeEventTypes(exchange.id, { enabled: streamed })
  const observations = detail.observations ?? []
  const sent = observations.filter((o) => o.side === 'request')
  const received = observations.filter((o) => o.side === 'response')
  const happened = observations.filter((o) => o.side !== 'request' && o.side !== 'response')
  const usage = exchange.usage
  const quality = linkQuality(exchange, detail.run.mode)

  return (
    <div className="space-y-6">
      {(exchange.degraded || detail.degradedReasons.length > 0) && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Captura degradada</AlertTitle>
          <AlertDescription>
            <p>Parte desta troca não foi capturada. O tráfego entre o cliente e a API não foi afetado.</p>
            {detail.degradedReasons.length > 0 && (
              <ul className="mt-1 list-disc pl-5">
                {detail.degradedReasons.map((reason, i) => (
                  <li key={i}>{reason}</li>
                ))}
              </ul>
            )}
          </AlertDescription>
        </Alert>
      )}

      <Fields
        label="Dados da troca"
        items={[
          { label: 'Endpoint', value: <span className="font-mono text-xs">{endpointLabel(exchange)}</span> },
          { label: 'Status HTTP', value: <HttpStatusBadge status={exchange.status} /> },
          { label: 'Estado', value: <StateBadge state={exchange.state} always /> },
          { label: 'Modelo pedido', value: exchange.requestedModel || <Absent>—</Absent> },
          { label: 'Modelo da resposta', value: exchange.responseModel || <Absent>—</Absent> },
          { label: 'Motivo de parada (stop_reason)', value: exchange.stopReason || <Absent>—</Absent> },
          { label: 'Duração total', value: formatMaybeMs(exchange.durationMs) ?? <Absent>não terminou</Absent> },
          { label: 'Tempo até o primeiro byte (TTFB)', value: formatMaybeMs(exchange.ttfbMs) ?? <Absent>—</Absent> },
          ...(exchange.stream
            ? [{ label: 'Primeiro evento SSE', value: formatMaybeMs(exchange.firstEventMs) ?? <Absent>—</Absent> }]
            : []),
          { label: 'Início', value: formatTimestamp(exchange.startedAt) },
          {
            label: 'Tamanhos (requisição / resposta)',
            value: `${formatBytes(exchange.requestBytes)} / ${formatBytes(exchange.responseBytes)}`,
          },
          { label: 'Streaming', value: exchange.stream ? `sim, ${formatCount(exchange.sseEvents)} eventos SSE` : 'não' },
          { label: 'Retries do cliente', value: formatCount(exchange.retryCount) },
          {
            label: 'Usage',
            value: usage ? (
              <Badge variant={usage.quality === 'final' ? 'success' : usage.quality === 'partial' ? 'warning' : 'outline'}>
                {USAGE_QUALITY_LABELS[usage.quality]}
              </Badge>
            ) : expectsUsage(exchange.kind) ? (
              <Badge variant="outline">{USAGE_QUALITY_LABELS.unavailable}</Badge>
            ) : (
              <span className="text-muted-foreground">não se aplica</span>
            ),
          },
          { label: 'Modo de captura', value: CAPTURE_MODE_LABELS[exchange.captureMode] ?? exchange.captureMode },
          {
            label: 'Sessão',
            value: exchange.sessionId ? (
              <Link to={sessionTrafficPath(exchange.sessionId)} className="font-mono text-xs underline-offset-4 hover:underline">
                {exchange.sessionId}
              </Link>
            ) : (
              <Absent>não correlacionada</Absent>
            ),
            hint: LINK_QUALITY_LABELS[quality],
          },
          {
            label: 'Execução de captura',
            value: (
              <Link to={runPath(detail.run.id)} className="underline-offset-4 hover:underline">
                {detail.run.name || detail.run.id}
              </Link>
            ),
          },
          ...(exchange.requestId ? [{ label: 'request-id', value: <span className="font-mono text-xs">{exchange.requestId}</span> }] : []),
          ...(exchange.errorType || exchange.error
            ? [{ label: 'Erro', value: <span className="text-error">{[exchange.errorType, exchange.error].filter(Boolean).join(': ')}</span> }]
            : []),
        ]}
      />

      {streamed && (
        <Section title="Tipos de evento observados">
          {eventTypes.isPending ? (
            <p className="text-sm text-muted-foreground">Carregando…</p>
          ) : eventTypes.isError ? (
            <p className="text-sm text-error">Não foi possível carregar os tipos de evento.</p>
          ) : (
            <ul className="flex flex-wrap gap-2" aria-label="Tipos de evento observados">
              {Object.entries(eventTypes.data ?? {}).map(([type, count]) => (
                <li key={type}>
                  <Badge variant="outline" className="font-mono">
                    {type} × {formatCount(count)}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </Section>
      )}

      <ObservationSection title="O que foi enviado?" observations={sent} empty="Nada interpretado no corpo da requisição." />
      <ObservationSection title="O que foi recebido?" observations={received} empty="Nada interpretado na resposta." />

      <Section title="O que aconteceu?">
        {happened.length > 0 && <ObservationList observations={happened} />}
        <ol className="space-y-1 text-sm" aria-label="Ciclo de vida da troca">
          {detail.lifecycle.map((event, i) => (
            <li key={i} className="flex flex-wrap items-baseline gap-x-3">
              <span className="w-24 shrink-0 font-mono text-xs text-muted-foreground tabular-nums" title={formatTimestamp(event.at)}>
                {formatClock(event.at)}
              </span>
              <span className="w-16 shrink-0 font-mono text-xs text-muted-foreground tabular-nums">{formatOffset(exchange.startedAt, event.at)}</span>
              <span className="font-medium">{lifecycleLabel(event.event)}</span>
              {event.detail && <span className="text-muted-foreground">{event.detail}</span>}
            </li>
          ))}
        </ol>
      </Section>
    </div>
  )
}

function ObservationSection({ title, observations, empty }: { title: string; observations: WireObservation[]; empty: string }) {
  return (
    <Section title={title}>
      {observations.length === 0 ? <p className="text-sm text-muted-foreground">{empty}</p> : <ObservationList observations={observations} />}
    </Section>
  )
}

function ObservationList({ observations }: { observations: WireObservation[] }) {
  return (
    <ul className="space-y-2">
      {observations.map((observation, i) => (
        <ObservationItem key={`${observation.code}:${i}`} observation={observation} />
      ))}
    </ul>
  )
}

function ObservationItem({ observation }: { observation: WireObservation }) {
  const [showEvidence, setShowEvidence] = useState(false)
  return (
    <li className="space-y-1 rounded-md border p-3">
      <div className="flex flex-wrap items-start gap-2">
        <p className="min-w-0 flex-1 text-sm">{observation.text}</p>
        <OriginBadge origin={observationOrigin(observation.certainty)} />
        <Badge variant="secondary">{CERTAINTY_LABELS[observation.certainty] ?? observation.certainty}</Badge>
      </div>
      {observation.evidence && (
        <>
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto p-0 text-xs"
            aria-expanded={showEvidence}
            onClick={() => setShowEvidence((v) => !v)}
          >
            {showEvidence ? 'Ocultar evidência' : 'Ver evidência'}
          </Button>
          {showEvidence && <p className="font-mono text-xs break-all text-muted-foreground">{observation.evidence}</p>}
        </>
      )}
    </li>
  )
}
