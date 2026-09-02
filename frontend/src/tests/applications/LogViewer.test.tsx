import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LogViewer } from '@/widgets/applications/LogViewer'

const journalAccess404 =
  '2026-08-30T19:23:01+0000 node-stats traefik[321740]: 20.104.50.220 - - [30/Aug/2026:19:23:01 +0000] "GET /wp-includes/post.php HTTP/1.1" 404 19 "-" "-" 8459 "-" "-" 0ms'
const journalAccess502 =
  '2026-08-30T19:23:02+0000 node-stats traefik[321740]: 10.0.0.1 - - [30/Aug/2026:19:23:02 +0000] "POST /api HTTP/1.1" 502 11 "-" "-" 8460 "-" "-" 3ms'
const dockerServiceErr = '2026-09-02T08:49:56Z ERR error="error while adding rule Host(`ro.test`)" entryPointName=web routerName=ro@file'
const appInfo = '2026/09/02 10:59:05 INFO system-stats: HTTP Request method=POST path=/api/v1/cluster/metrics status=200'

describe('LogViewer line rendering', () => {
  it('colours access-log lines by HTTP status and dims journal prefixes', () => {
    render(<LogViewer logs={[journalAccess404, journalAccess502].join('\n')} />)
    const l404 = screen.getByText(/GET \/wp-includes\/post\.php/)
    expect(l404.className).toContain('text-amber-400')
    const l502 = screen.getByText(/POST \/api HTTP/)
    expect(l502.className).toContain('text-red-400')
    // The journal timestamp + "host unit[pid]:" ident are split off as the dim prefix.
    const prefix = screen.getByText(/2026-08-30T19:23:01\+0000 node-stats traefik\[321740\]:/)
    expect(prefix.className).toContain('text-muted-foreground')
    expect(l404.textContent).not.toContain('traefik[321740]')
  })

  it('keeps level detection for Traefik service and app lines', () => {
    render(<LogViewer logs={[dockerServiceErr, appInfo].join('\n')} />)
    expect(screen.getByText('ERR').className).toContain('text-red-400')
    expect(screen.getByText('INFO').className).toContain('text-emerald-400/90')
    expect(screen.getByText(/2026-09-02T08:49:56Z/).className).toContain('text-muted-foreground')
  })
})
