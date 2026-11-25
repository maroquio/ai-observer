import type { ReactNode } from 'react'
import { NavLink, matchPath, useLocation } from 'react-router-dom'
import { GitBranch, BarChart3, FileText, BookOpen, ChevronDown, MessageSquare, Activity, Network, Home, type LucideIcon } from 'lucide-react'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuButton,
  useSidebar,
} from '@/components/ui/sidebar'
import { useDataSources } from '@/hooks/useDataSources'
import type { FeatureVisibility } from '@/lib/dataSources'
import { useStatus } from '@/lib/queries'
import { SidebarDashboards } from './SidebarDashboards'

interface NavItem {
  to: string
  icon: LucideIcon
  label: string
  // Shown only when this source has data (lib/dataSources.ts)
  feature?: keyof FeatureVisibility
  // Highlighted on this path only, not on the pages below it
  end?: boolean
}

// The home page's entry while it shows the data sources instead of a
// dashboard (the Dashboards group is hidden then)
const homeItem: NavItem = { to: '/', icon: Home, label: 'Home', end: true }

const insightsItems: NavItem[] = [
  { to: '/metrics', icon: BarChart3, label: 'Metrics', feature: 'metrics' },
  { to: '/logs', icon: FileText, label: 'Logs', feature: 'logs' },
  { to: '/traces', icon: GitBranch, label: 'Traces', feature: 'traces' },
  { to: '/sessions', icon: MessageSquare, label: 'Sessions', feature: 'sessions' },
  { to: '/http-inspector', icon: Network, label: 'HTTP Inspector', feature: 'httpInspector' },
]

const systemItems: NavItem[] = [
  { to: '/status', icon: Activity, label: 'Status' },
  { to: '/docs', icon: BookOpen, label: 'Documentation' },
]

// A page's link: highlighted on the page and the pages below it (a trace
// below Traces); closes the sidebar on phones. Entries of a source without
// data are left out, except the one of the page shown (opened by a link).
function NavItems({ items, features }: { items: NavItem[]; features?: FeatureVisibility }) {
  const { pathname } = useLocation()
  const { isMobile, setOpenMobile } = useSidebar()

  return (
    <SidebarMenu>
      {items.map((item) => {
        const isActive = matchPath({ path: item.to, end: item.end ?? false }, pathname) !== null
        if (item.feature && features && !features[item.feature] && !isActive) {
          return null
        }
        return (
          <SidebarMenuItem key={item.to}>
            <SidebarMenuButton asChild isActive={isActive}>
              {/* NavLink marks the current page with aria-current="page" */}
              <NavLink to={item.to} end={item.end} onClick={() => isMobile && setOpenMobile(false)}>
                <item.icon className="h-4 w-4" aria-hidden="true" />
                <span>{item.label}</span>
              </NavLink>
            </SidebarMenuButton>
          </SidebarMenuItem>
        )
      })}
    </SidebarMenu>
  )
}

function NavGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Collapsible defaultOpen className="group/collapsible">
      <SidebarGroup>
        <SidebarGroupLabel asChild>
          <CollapsibleTrigger className="flex w-full items-center">
            {label}
            <ChevronDown className="ml-auto h-4 w-4 transition-transform group-data-[state=open]/collapsible:rotate-180" />
          </CollapsibleTrigger>
        </SidebarGroupLabel>
        <CollapsibleContent>
          <SidebarGroupContent>{children}</SidebarGroupContent>
        </CollapsibleContent>
      </SidebarGroup>
    </Collapsible>
  )
}

// The server's version, from the status the status page shows too
function VersionFooter() {
  const status = useStatus()
  const { isMobile, setOpenMobile } = useSidebar()
  if (!status.data) {
    return null
  }
  const version = status.data.version
  return (
    <SidebarFooter>
      <NavLink
        to="/status"
        onClick={() => isMobile && setOpenMobile(false)}
        className="block rounded-md px-2 py-1 text-xs text-muted-foreground hover:text-foreground"
        title="Server status"
      >
        AI Observer {/^\d/.test(version) ? `v${version}` : version}
        {status.data.mode === 'watch' && ' · watch mode'}
      </NavLink>
    </SidebarFooter>
  )
}

export function AppSidebar() {
  const { features } = useDataSources()
  const { pathname } = useLocation()
  // Without telemetry the home page shows the data sources, not a dashboard
  const showDashboards = features.dashboards || matchPath({ path: '/dashboard/:id' }, pathname) !== null

  return (
    <Sidebar collapsible="offcanvas">
      <SidebarContent>
        {showDashboards && (
          <NavGroup label="Dashboards">
            <SidebarDashboards />
          </NavGroup>
        )}
        <NavGroup label="Insights">
          <NavItems items={showDashboards ? insightsItems : [homeItem, ...insightsItems]} features={features} />
        </NavGroup>
        <NavGroup label="System">
          <NavItems items={systemItems} />
        </NavGroup>
      </SidebarContent>
      <VersionFooter />
    </Sidebar>
  )
}
