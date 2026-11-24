import type { ReactNode } from 'react'
import { Coins } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { EmptyState } from '@/components/common/empty-state'
import { ListSkeleton } from '@/components/common/list-skeleton'
import { QueryError } from '@/components/common/query-error'
import { formatCost, formatCount } from '@/lib/format'
import { COST_STATUS_LABELS, USAGE_QUALITY_LABELS, costComponentLabel, formatShare, type DataOrigin } from '@/lib/httpInspector'
import { useHTTPExchangeUsage } from '@/lib/queries'
import type { HTTPUsageDetail, WireUsage } from '@/types/httpInspector'
import { Absent, Expandable, JsonBlock, OriginBadge, Section } from '../primitives'

const QUALITY_HINTS: Record<string, string> = {
  final: 'A resposta terminou normalmente: estes são os números finais informados pela API.',
  partial: 'A resposta foi interrompida: estes são os últimos valores cumulativos observados, não os finais.',
  unavailable: 'A resposta não trouxe usage.',
}

/** A count that may be absent: null/undefined is "ausente", never 0 */
function maybe(n: number | null | undefined): ReactNode {
  return n == null ? <Absent /> : formatCount(n)
}

/** Tokens & Cache: the usage fields as reported, the totals derived from them, their evolution in the stream and the estimated cost */
export function UsageTab({ exchangeId }: { exchangeId: string }) {
  const query = useHTTPExchangeUsage(exchangeId)
  if (query.isPending) {
    return <ListSkeleton rows={5} label="Carregando usage" />
  }
  if (query.isError) {
    return <QueryError error={query.error} title="Não foi possível carregar o usage" onRetry={() => void query.refetch()} retrying={query.isFetching} />
  }
  if (!query.data) {
    return (
      <EmptyState
        icon={Coins}
        title="Usage indisponível"
        description="Esta troca não tem usage: não houve resposta do modelo (erro, outro endpoint ou resposta interrompida antes do message_start)."
      />
    )
  }
  return <UsageDetail usage={query.data} />
}

function UsageDetail({ usage }: { usage: HTTPUsageDetail }) {
  const u = usage.usage
  const t = usage.totals
  const isCount = usage.countTokens != null

  const rows: Array<{ field: string; value: ReactNode; origin: DataOrigin; note?: string }> = [
    { field: 'input_tokens', value: maybe(u.inputTokens), origin: 'wire', note: 'Entrada que não veio do cache' },
    { field: 'output_tokens', value: maybe(u.outputTokens), origin: 'wire' },
    { field: 'cache_read_input_tokens', value: maybe(u.cacheReadInputTokens), origin: 'wire', note: 'Lidos do cache' },
    { field: 'cache_creation_input_tokens', value: maybe(u.cacheCreationInputTokens), origin: 'wire', note: 'Gravados no cache' },
    { field: '└ ephemeral_5m_input_tokens', value: maybe(u.cacheCreation5m), origin: 'wire', note: 'Parte com duração de 5 minutos' },
    { field: '└ ephemeral_1h_input_tokens', value: maybe(u.cacheCreation1h), origin: 'wire', note: 'Parte com duração de 1 hora' },
    {
      field: '└ duração não informada',
      // Left out when nothing is missing; unknowable without cache_creation
      value: u.cacheCreationInputTokens == null ? <Absent /> : formatCount(t.cacheWriteUnknown ?? 0),
      origin: 'derivado',
      note: 'cache_creation − 5m − 1h (escrita sem duração informada pela API)',
    },
    ...(u.serviceTier ? [{ field: 'service_tier', value: u.serviceTier, origin: 'wire' as const }] : []),
  ]

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={usage.quality === 'final' ? 'success' : usage.quality === 'partial' ? 'warning' : 'outline'}>
          {USAGE_QUALITY_LABELS[usage.quality] ?? usage.quality}
        </Badge>
        <span className="text-sm text-muted-foreground">{QUALITY_HINTS[usage.quality]}</span>
      </div>

      {isCount && (
        <Section title="count_tokens">
          <p className="text-sm">
            <span className="text-2xl font-bold tabular-nums">{formatCount(usage.countTokens ?? 0)}</span> tokens de entrada estimados.
          </p>
          <p className="text-xs text-muted-foreground">
            Resposta de POST /v1/messages/count_tokens: uma estimativa do tamanho da entrada, sem geração. Não é consumo e não entra nos totais.
          </p>
        </Section>
      )}

      <Section title="Campos de usage" actions={<span className="text-xs text-muted-foreground">“ausente” = não informado (diferente de 0)</span>}>
        <div className="overflow-x-auto rounded-md border">
          <Table aria-label="Campos de usage">
            <TableHeader>
              <TableRow>
                <TableHead>Campo</TableHead>
                <TableHead className="text-right">Tokens</TableHead>
                <TableHead>Origem</TableHead>
                <TableHead>Observação</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.field}>
                  <TableCell className="font-mono text-xs">{row.field}</TableCell>
                  <TableCell className="text-right tabular-nums">{row.value}</TableCell>
                  <TableCell>
                    <OriginBadge origin={row.origin} />
                  </TableCell>
                  <TableCell className="text-xs whitespace-normal text-muted-foreground">{row.note}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </Section>

      <Section title="Totais derivados" actions={<OriginBadge origin="derivado" />}>
        <dl className="space-y-2 text-sm">
          <Formula label="total_input" value={maybe(t.totalInput)} formula="input_tokens + cache_read_input_tokens + cache_creation_input_tokens" />
          <Formula label="total_processed" value={maybe(t.totalProcessed)} formula="total_input + output_tokens" />
          <Formula label="cache_read_share" value={formatShare(t.cacheReadShare) ?? <Absent />} formula="cache_read_input_tokens ÷ total_input" />
        </dl>
        {!t.complete && (
          <p className="text-xs text-warning">
            Nem todos os campos de entrada foram informados: campos ausentes não entram nos totais (não são tratados como 0).
          </p>
        )}
      </Section>

      <Section title="Evolução no stream">
        {usage.snapshots.length === 0 ? (
          <p className="text-sm text-muted-foreground">Sem snapshots: a resposta não veio por streaming ou não trouxe usage nos eventos.</p>
        ) : (
          <>
            <p className="text-xs text-muted-foreground">
              Cada evento traz o usage acumulado até ali: o último é a referência. Os valores nunca são somados entre si.
            </p>
            <div className="overflow-x-auto rounded-md border">
              <Table aria-label="Evolução do usage no stream">
                <TableHeader>
                  <TableRow>
                    <TableHead>Evento</TableHead>
                    <TableHead>Tipo</TableHead>
                    <TableHead className="text-right">input</TableHead>
                    <TableHead className="text-right">output</TableHead>
                    <TableHead className="text-right">cache read</TableHead>
                    <TableHead className="text-right">cache write</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {usage.snapshots.map((snapshot) => (
                    <SnapshotRow key={snapshot.eventSeq} seq={snapshot.eventSeq} type={snapshot.eventType} usage={snapshot.usage} />
                  ))}
                </TableBody>
              </Table>
            </div>
          </>
        )}
      </Section>

      <Section title="Custo equivalente de API (estimativa)" actions={<OriginBadge origin="derivado" />}>
        <div className="flex flex-wrap items-baseline gap-3">
          <span className="text-2xl font-bold tabular-nums">
            {usage.costUsd != null ? formatCost(usage.costUsd) : <Absent>não estimável</Absent>}
          </span>
          <Badge variant={usage.costStatus === 'complete' ? 'success' : usage.costStatus === 'partial' ? 'warning' : 'outline'}>
            {COST_STATUS_LABELS[usage.costStatus] ?? usage.costStatus}
          </Badge>
          {usage.pricedModel && <span className="text-sm text-muted-foreground">preço de {usage.pricedModel}</span>}
        </div>
        {usage.costReason && <p className="text-sm text-muted-foreground">{usage.costReason}</p>}
        {usage.costComponents.length > 0 && (
          <div className="overflow-x-auto rounded-md border">
            <Table aria-label="Componentes do custo">
              <TableHeader>
                <TableRow>
                  <TableHead>Componente</TableHead>
                  <TableHead className="text-right">Tokens</TableHead>
                  <TableHead className="text-right">US$ / milhão</TableHead>
                  <TableHead className="text-right">Custo</TableHead>
                  <TableHead>Observação</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {usage.costComponents.map((c) => (
                  <TableRow key={c.component}>
                    <TableCell>{costComponentLabel(c.component)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatCount(c.tokens)}</TableCell>
                    <TableCell className="text-right tabular-nums">{c.pricePerMTok != null ? `$${c.pricePerMTok}` : <Absent>sem preço</Absent>}</TableCell>
                    <TableCell className="text-right tabular-nums">{c.costUsd != null ? formatCost(c.costUsd) : <Absent>—</Absent>}</TableCell>
                    <TableCell className="text-xs whitespace-normal text-muted-foreground">{c.note}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        <p className="text-xs text-muted-foreground">
          custo = tokens × preço por milhão ÷ 1.000.000, por componente, com o catálogo de preços do AI Observer. É o valor equivalente na API,
          não a cobrança de uma assinatura (OAuth, planos Pro/Max). Fonte do usage: {usage.source === 'http_proxy' ? 'capturado na rede pelo proxy' : usage.source}.
        </p>
      </Section>

      {u.raw !== undefined && (
        <Expandable title="Objeto usage como recebido">
          <JsonBlock value={u.raw} label="usage original" />
        </Expandable>
      )}
    </div>
  )
}

function Formula({ label, value, formula }: { label: string; value: ReactNode; formula: string }) {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
      <dt className="font-mono text-xs">{label}</dt>
      <dd className="font-medium tabular-nums">{value}</dd>
      <dd className="w-full font-mono text-xs text-muted-foreground">= {formula}</dd>
    </div>
  )
}

function SnapshotRow({ seq, type, usage }: { seq: number; type: string; usage: WireUsage }) {
  return (
    <TableRow>
      <TableCell className="font-mono text-xs">#{seq}</TableCell>
      <TableCell className="font-mono text-xs">{type}</TableCell>
      <TableCell className="text-right tabular-nums">{maybe(usage.inputTokens)}</TableCell>
      <TableCell className="text-right tabular-nums">{maybe(usage.outputTokens)}</TableCell>
      <TableCell className="text-right tabular-nums">{maybe(usage.cacheReadInputTokens)}</TableCell>
      <TableCell className="text-right tabular-nums">{maybe(usage.cacheCreationInputTokens)}</TableCell>
    </TableRow>
  )
}
