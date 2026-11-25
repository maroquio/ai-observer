import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { FileText, Network, Radio, type LucideIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { CopyButton } from '@/components/common/copy-button'
import { PageHeader } from '@/components/common/page-header'
import type { StatusResponse } from '@/types/status'

interface DataSourcesWelcomeProps {
  status: StatusResponse
  // Captures stored while the proxy is off (undefined: not known)
  capturesStored?: boolean
}

/**
 * The home page while no telemetry is stored: the three data sources, which
 * of them are on, and how to turn on the others. The page becomes the
 * dashboard when the first telemetry arrives.
 */
export function DataSourcesWelcome({ status, capturesStored }: DataSourcesWelcomeProps) {
  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title="Fontes de dados"
        description="Nenhuma telemetria recebida ainda. As fontes abaixo são independentes: cada página do menu aparece quando a sua fonte envia dados."
      />
      <div className="grid max-w-4xl gap-4">
        <ProxySource status={status} capturesStored={capturesStored} />
        <OTLPSource status={status} />
        <SessionFilesSource status={status} />
      </div>
      <p className="text-sm text-muted-foreground">Quando chegar a primeira telemetria, esta página dá lugar ao Dashboard.</p>
    </div>
  )
}

function ProxySource({ status, capturesStored }: DataSourcesWelcomeProps) {
  const inspector = status.httpInspector
  const enabled = inspector?.enabled === true
  const state = enabled
    ? { label: 'Ligado', variant: 'success' as const }
    : { label: capturesStored ? 'Desligado, com capturas guardadas' : 'Desligado', variant: 'outline' as const }

  return (
    <SourceCard
      icon={Network}
      title="Tráfego da API"
      state={state}
      description="As requisições e respostas do Claude Code à API da Anthropic, capturadas por um proxy local. Alimenta o HTTP Inspector."
      link={enabled || capturesStored ? { to: '/http-inspector', label: 'Abrir o HTTP Inspector' } : undefined}
    >
      {enabled && inspector.proxyUrl ? (
        <Commands intro="Aponte o Claude Code para o proxy:" commands={`export ANTHROPIC_BASE_URL=${inspector.proxyUrl}`} label="Copiar o comando do proxy" />
      ) : enabled ? (
        <Commands intro="Rode o Claude Code pelo launcher:" commands="ai-observer run -- claude" label="Copiar o comando do launcher" />
      ) : (
        <Commands intro="Inicie o servidor com o proxy de captura:" commands="ai-observer serve --http-proxy" label="Copiar o comando do servidor" />
      )}
    </SourceCard>
  )
}

function OTLPSource({ status }: { status: StatusResponse }) {
  const enabled = status.server?.otlpEnabled !== false
  const port = status.server?.otlpPort || 4318
  const commands = [
    'export CLAUDE_CODE_ENABLE_TELEMETRY=1',
    'export OTEL_METRICS_EXPORTER=otlp',
    'export OTEL_LOGS_EXPORTER=otlp',
    'export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf',
    `export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:${port}`,
    'export OTEL_LOG_USER_PROMPTS=1',
  ].join('\n')

  return (
    <SourceCard
      icon={Radio}
      title="Telemetria OTLP"
      state={enabled ? { label: 'Aguardando dados', variant: 'warning' } : { label: 'Desligada', variant: 'outline' }}
      description="Métricas, logs e traces que a própria ferramenta envia. Alimenta Dashboards, Metrics, Logs, Traces e Sessions."
    >
      {enabled ? (
        <>
          <Commands intro="No shell onde o Claude Code vai rodar:" commands={commands} label="Copiar as variáveis da telemetria" />
          <p className="text-xs text-muted-foreground">
            Outras ferramentas (Codex, Gemini CLI, Copilot, OpenCode): <code className="font-mono">ai-observer setup &lt;ferramenta&gt;</code>.
          </p>
        </>
      ) : (
        <Commands
          intro="O servidor roda no modo watch, que não recebe OTLP. Inicie-o com:"
          commands="ai-observer serve"
          label="Copiar o comando do servidor"
        />
      )}
    </SourceCard>
  )
}

function SessionFilesSource({ status }: { status: StatusResponse }) {
  const watching = status.mode === 'watch' || status.mode === 'serve+watch'
  const tools = status.watchedTools ?? []

  return (
    <SourceCard
      icon={FileText}
      title="Arquivos de sessão"
      state={watching ? { label: 'Acompanhando, sem registros', variant: 'warning' } : { label: 'Desligado', variant: 'outline' }}
      description="Os arquivos que Claude Code, Codex e Gemini CLI gravam no disco, lidos como os mesmos logs e métricas da telemetria."
    >
      {watching ? (
        <p className="text-sm">
          Lendo {tools.length > 0 ? tools.join(', ') : 'os arquivos de sessão'}. Sem <code className="font-mono">--backfill</code>, só
          entram as mudanças feitas depois que o servidor iniciou.
        </p>
      ) : (
        <Commands
          intro="Importe uma vez, ou acompanhe as mudanças junto com o servidor:"
          commands={'ai-observer import claude-code\nai-observer serve --watch'}
          label="Copiar os comandos dos arquivos de sessão"
        />
      )}
    </SourceCard>
  )
}

interface SourceCardProps {
  icon: LucideIcon
  title: string
  state: { label: string; variant: 'success' | 'warning' | 'outline' }
  description: string
  link?: { to: string; label: string }
  children: ReactNode
}

function SourceCard({ icon: Icon, title, state, description, link, children }: SourceCardProps) {
  return (
    <Card role="region" aria-label={title}>
      <CardHeader className="space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <CardTitle className="flex items-center gap-2 text-base">
            <Icon className="h-4 w-4" aria-hidden="true" />
            {title}
          </CardTitle>
          <Badge variant={state.variant}>{state.label}</Badge>
        </div>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {children}
        {link && (
          <Link to={link.to} className="inline-block text-sm font-medium underline underline-offset-4">
            {link.label}
          </Link>
        )}
      </CardContent>
    </Card>
  )
}

function Commands({ intro, commands, label }: { intro: string; commands: string; label: string }) {
  return (
    <div className="space-y-2">
      <p className="text-sm">{intro}</p>
      <div className="relative">
        <pre className="overflow-x-auto rounded-md border bg-muted p-3 pr-10 font-mono text-xs">{commands}</pre>
        <CopyButton value={commands} label={label} className="absolute right-1.5 top-1.5" />
      </div>
    </div>
  )
}
