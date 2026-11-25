import { Skeleton } from '@/components/ui/skeleton'
import { DataSourcesWelcome } from '@/components/home/DataSourcesWelcome'
import { useDataSources } from '@/hooks/useDataSources'
import { hasTelemetry } from '@/lib/dataSources'
import { Dashboard } from '@/pages/Dashboard'

/**
 * The home page: the default dashboard, or, while no telemetry is stored,
 * the data sources and how to turn them on. A status that failed or lacks
 * the counts shows the dashboard, as before.
 */
export function HomePage() {
  const { status, presence, capturesStored } = useDataSources()

  if (status.isPending) {
    return (
      <div className="space-y-4 p-6" aria-busy="true" aria-label="Carregando">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }
  if (status.data && presence && !hasTelemetry(presence)) {
    return <DataSourcesWelcome status={status.data} capturesStored={capturesStored} />
  }
  return <Dashboard />
}
