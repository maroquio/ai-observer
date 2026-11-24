import { BrowserRouter } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { ThemeProvider } from '@/components/theme-provider'
import { SidebarProvider } from '@/components/ui/sidebar'
import { Toaster } from '@/components/ui/sonner'
import { AppRoutes } from '@/AppRoutes'
import { queryClient } from '@/lib/queryClient'

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider defaultTheme="system" storageKey="ai-observer-theme">
        <SidebarProvider>
          <BrowserRouter>
            <AppRoutes />
          </BrowserRouter>
          <Toaster />
        </SidebarProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
}

export default App
