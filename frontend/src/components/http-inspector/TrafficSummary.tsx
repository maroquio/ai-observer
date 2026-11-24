import type { ReactNode } from 'react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { formatCost, formatCount, formatDurationMs } from '@/lib/format'
import {
  COST_STATUS_LABELS,
  LINK_QUALITY_DESCRIPTIONS,
  LINK_QUALITY_LABELS,
  comparisonCertaintyLabel,
  formatShare,
  linkQualityCounts,
  type LinkQuality,
} from '@/lib/httpInspector'
import { cn, formatTimestamp } from '@/lib/utils'
import type { HTTPCaptureRun, HTTPExchange, HTTPSourceComparison, HTTPTrafficSummary } from '@/types/httpInspector'
import { Absent, OriginBadge } from './primitives'

interface TrafficSummaryProps {
  summary: HTTPTrafficSummary
  // The loaded exchanges, for how they were linked to their session
  exchanges?: HTTPExchange[]
  // Whether exchanges holds all of them (else the counts say "carregadas")
  allLoaded?: boolean
  runs?: Pick<HTTPCaptureRun, 'id' | 'mode'>[]
  // A session's comparison with its telemetry; showComparison shows the
  // card also without one (a session without inference exchanges)
  comparison?: HTTPSourceComparison
  showComparison?: boolean
}

/**
 * The summary cards of a session's or run's API traffic. Token counts are
 * the proxy's own observation and are never added to the OTLP or session
 * file totals shown elsewhere.
 */
export function TrafficSummary({ summary, exchanges, allLoaded = true, runs, comparison, showComparison = false }: TrafficSummaryProps) {
  const tokens = summary.tokens
  const partial = summary.partialTokens
  const cacheShare = formatShare(tokens.cacheReadShare)

  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" aria-label="Resumo do tráfego">
      <SummaryCard title="Trocas" origin="wire">
        <p className="text-3xl font-bold tabular-nums">{formatCount(summary.exchanges)}</p>
        <Lines
          items={[
            ['Inferência (/v1/messages)', formatCount(summary.inference)],
            ['Contagem de tokens (count_tokens)', formatCount(summary.tokenCounts)],
            ...(summary.quotaProbes > 0
              ? ([['Sondas de cota do Claude Code (auxiliares)', formatCount(summary.quotaProbes)]] as Array<[string, ReactNode]>)
              : []),
            ['Outros endpoints', formatCount(summary.other)],
            ['Respostas em streaming (SSE)', formatCount(summary.streams)],
            ...(summary.inProgress > 0 ? ([['Em andamento', formatCount(summary.inProgress)]] as Array<[string, ReactNode]>) : []),
          ]}
        />
      </SummaryCard>

      <SummaryCard title="Erros, retries e parciais" origin="wire">
        <Lines
          items={[
            ['Erros (4xx/5xx ou sem resposta)', <Count n={summary.errors} tone="error" />],
            ['Retries do cliente', <Count n={summary.retries} tone="warning" />],
            ['Streams ou respostas parciais', <Count n={summary.partial} tone="warning" />],
            ['Captura degradada', <Count n={summary.degraded} tone="warning" />],
          ]}
        />
        {summary.degraded > 0 && (
          <p className="text-xs text-muted-foreground">
            Captura degradada: parte dos dados não foi gravada; o tráfego seguiu normalmente.
          </p>
        )}
      </SummaryCard>

      <SummaryCard title="Tokens (usage final)" origin="wire">
        {tokens.exchanges === 0 ? (
          <p className="text-sm text-muted-foreground">Nenhuma troca de inferência com usage final.</p>
        ) : (
          <Lines
            items={[
              ['Entrada sem cache (input_tokens)', formatCount(tokens.input)],
              ['Saída (output_tokens)', formatCount(tokens.output)],
              ['Leitura de cache', formatCount(tokens.cacheRead)],
              [
                'Escrita de cache',
                <span>
                  {formatCount(tokens.cacheWrite)}
                  <span className="block text-xs font-normal text-muted-foreground">
                    5m: {formatCount(tokens.cacheWrite5m)} · 1h: {formatCount(tokens.cacheWrite1h)}
                    {tokens.cacheWriteOther > 0 && <> · sem duração: {formatCount(tokens.cacheWriteOther)}</>}
                  </span>
                </span>,
              ],
              [
                <span className="inline-flex items-center gap-1">
                  Entrada total <OriginBadge origin="derivado" />
                </span>,
                <span>
                  {formatCount(tokens.totalInput)}
                  {cacheShare && <span className="block text-xs font-normal text-muted-foreground">{cacheShare} lido do cache</span>}
                </span>,
              ],
            ]}
          />
        )}
        <p className="text-xs text-muted-foreground">
          {formatCount(tokens.exchanges)} {tokens.exchanges === 1 ? 'troca com usage final' : 'trocas com usage final'}.
          {partial.exchanges > 0 && (
            <>
              {' '}
              Outras {formatCount(partial.exchanges)} com usage parcial ficam à parte (entrada total {formatCount(partial.totalInput)}, saída{' '}
              {formatCount(partial.output)}).
            </>
          )}
          {summary.countedTokens > 0 && <> Estimativas de count_tokens: {formatCount(summary.countedTokens)} (não é consumo).</>}
        </p>
      </SummaryCard>

      <SummaryCard title="Tempo e custo" origin="derivado">
        <Lines
          items={[
            ['Tempo total (primeira requisição → última resposta)', summary.durationMs > 0 ? formatDurationMs(summary.durationMs) : <Absent>—</Absent>],
            ...(summary.firstAt ? ([['Início', formatTimestamp(summary.firstAt)]] as Array<[string, ReactNode]>) : []),
            [
              'Custo equivalente de API (estimativa)',
              summary.costStatus === 'unavailable' ? (
                <Absent title={COST_STATUS_LABELS.unavailable}>não estimável</Absent>
              ) : (
                <span>
                  {formatCost(summary.costUsd)}
                  {summary.costStatus === 'partial' && (
                    <Badge variant="warning" className="ml-2">
                      parcial
                    </Badge>
                  )}
                </span>
              ),
            ],
          ]}
        />
        <p className="text-xs text-muted-foreground">
          Estimado com o catálogo de preços da API para comparação; não é a cobrança de uma assinatura (OAuth, planos Pro/Max).
          {summary.costStatus === 'partial' && ' Parte das trocas não pôde ser precificada.'}
        </p>
        {summary.models.length > 0 && (
          <p className="text-xs text-muted-foreground">
            Modelos: <span className="font-mono">{summary.models.join(', ')}</span>
          </p>
        )}
      </SummaryCard>

      {exchanges && <LinkQualityCard exchanges={exchanges} runs={runs} allLoaded={allLoaded} />}

      {(comparison || showComparison) && <ComparisonCard comparison={comparison} />}
    </div>
  )
}

function SummaryCard({ title, origin, children }: { title: string; origin?: 'wire' | 'derivado'; children: ReactNode }) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0 pb-3">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        {origin && <OriginBadge origin={origin} />}
      </CardHeader>
      <CardContent className="space-y-2">{children}</CardContent>
    </Card>
  )
}

function Lines({ items }: { items: Array<[ReactNode, ReactNode]> }) {
  return (
    <dl className="space-y-1 text-sm">
      {items.map(([label, value], index) => (
        <div key={index} className="flex items-start justify-between gap-3">
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="text-right font-medium tabular-nums">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function Count({ n, tone }: { n: number; tone: 'error' | 'warning' }) {
  return <span className={cn(n > 0 && (tone === 'error' ? 'text-error' : 'text-warning'))}>{formatCount(n)}</span>
}

const QUALITY_ORDER: LinkQuality[] = ['header', 'metadata', 'launcher', 'none']

function LinkQualityCard({ exchanges, runs, allLoaded }: { exchanges: HTTPExchange[]; runs?: Pick<HTTPCaptureRun, 'id' | 'mode'>[]; allLoaded: boolean }) {
  const counts = linkQualityCounts(exchanges, runs)
  return (
    <SummaryCard title="Vínculo com a sessão" origin="wire">
      <dl className="space-y-1 text-sm">
        {QUALITY_ORDER.map((quality) => (
          <div key={quality} className="flex items-start justify-between gap-3" title={LINK_QUALITY_DESCRIPTIONS[quality]}>
            <dt className="text-muted-foreground">{LINK_QUALITY_LABELS[quality]}</dt>
            <dd className={cn('font-medium tabular-nums', quality === 'none' && counts.none > 0 && 'text-warning')}>{formatCount(counts[quality])}</dd>
          </div>
        ))}
      </dl>
      <p className="text-xs text-muted-foreground">
        Evidência que liga cada troca à sessão: o cabeçalho é a ligação direta; metadata.user_id vem do corpo da requisição.
        {!allLoaded && ` Contagem das ${formatCount(exchanges.length)} trocas carregadas.`}
      </p>
    </SummaryCard>
  )
}

function ComparisonCard({ comparison }: { comparison?: HTTPSourceComparison }) {
  return (
    <Card className="md:col-span-2 xl:col-span-3">
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 space-y-0 pb-3">
        <CardTitle className="text-sm font-medium">Comparação com a telemetria (OTLP / arquivo de sessão)</CardTitle>
        <div className="flex items-center gap-1">
          <OriginBadge origin="wire" />
          <OriginBadge origin="otel" />
          <OriginBadge origin="jsonl" />
          <Badge variant="secondary">não somado</Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-2">
        {!comparison ? (
          <p className="text-sm text-muted-foreground">Sem trocas de inferência para comparar com a telemetria.</p>
        ) : (
          <>
            <Lines
              items={[
                ['Certeza da correspondência', <Badge variant={comparison.certainty === 'exact' ? 'success' : comparison.certainty === 'partial' ? 'warning' : 'outline'}>{comparisonCertaintyLabel(comparison.certainty)}</Badge>],
                ['Requisições com o mesmo request_id nas duas fontes', formatCount(comparison.matchedRequests)],
                ['Proxy: entrada total / saída', `${formatCount(comparison.proxyInputTokens)} / ${formatCount(comparison.proxyOutputTokens)}`],
                [
                  'Telemetria: entrada / saída',
                  comparison.telemetryInputTokens == null && comparison.telemetryOutputTokens == null ? (
                    <Absent>não informado</Absent>
                  ) : (
                    `${comparison.telemetryInputTokens != null ? formatCount(comparison.telemetryInputTokens) : '—'} / ${comparison.telemetryOutputTokens != null ? formatCount(comparison.telemetryOutputTokens) : '—'}`
                  ),
                ],
              ]}
            />
            {comparison.note && <p className="text-sm">{comparison.note}</p>}
          </>
        )}
        <p className="text-xs text-muted-foreground">
          As duas fontes descrevem o mesmo consumo: os números do proxy aparecem só aqui e nunca são somados aos totais de OTLP ou JSONL do AI
          Observer.
        </p>
      </CardContent>
    </Card>
  )
}
