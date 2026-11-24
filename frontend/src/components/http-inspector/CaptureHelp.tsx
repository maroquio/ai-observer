import type { ReactNode } from 'react'
import { Network } from 'lucide-react'
import { EmptyState } from '@/components/common/empty-state'

// The commands that start a capture (static text)
const COMMANDS = 'ai-observer serve --http-proxy\nai-observer run -- claude'

interface CaptureHelpProps {
  title: ReactNode
  // Text above the commands
  intro?: ReactNode
  className?: string
}

/**
 * How to capture API traffic: shown where nothing was captured, instead of
 * rows that would look like traffic
 */
export function CaptureHelp({ title, intro, className }: CaptureHelpProps) {
  return (
    <EmptyState
      icon={Network}
      title={title}
      className={className}
      description={
        <div className="space-y-3 text-left">
          <p>
            {intro ??
              'O HTTP Inspector vem desligado. Para ver as requisições que o Claude Code faz à API da Anthropic, inicie o servidor com o proxy de captura e rode o Claude Code pelo launcher:'}
          </p>
          <pre className="rounded-md border bg-muted p-3 font-mono text-xs text-foreground" aria-label="Comandos para capturar o tráfego">
            {COMMANDS}
          </pre>
          <p>
            Só aparecem aqui trocas reais observadas pelo proxy e ligadas a esta sessão; nada é simulado. Credenciais nunca são gravadas, e o
            modo padrão (safe) mascara segredos nos corpos.
          </p>
        </div>
      }
    />
  )
}
