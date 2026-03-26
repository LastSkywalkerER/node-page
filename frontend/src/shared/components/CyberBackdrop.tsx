import { createPortal } from 'react-dom'
import cyberDarkUrl from '@/assets/backgrounds/cyber-atmosphere-dark.jpg?url'
import cyberLightUrl from '@/assets/backgrounds/cyber-atmosphere-light.jpg?url'

/** Resolve hashed /assets URLs when app is served under a non-root VITE_BASE. */
function withPublicBase(href: string): string {
  if (href.startsWith('http') || href.startsWith('data:')) return href
  const path = href.startsWith('/') ? href : `/${href}`
  const base = import.meta.env.BASE_URL ?? '/'
  if (base === '/' || base === '') return path
  const root = base.endsWith('/') ? base.slice(0, -1) : base
  return `${root}${path}`
}

const SRC_LIGHT = withPublicBase(cyberLightUrl)
const SRC_DARK = withPublicBase(cyberDarkUrl)

function imgDebugHandlers(label: 'light' | 'dark', src: string) {
  if (!import.meta.env.DEV) return {}
  return {
    onLoad: () => console.debug('[CyberBackdrop]', label, 'loaded', src),
    onError: () => console.error('[CyberBackdrop]', label, 'FAILED — check Network tab', src),
  } as const
}

/**
 * Full-viewport ambient layers behind UI. Portals to document.body so `position:fixed`
 * is not affected by ancestor stacking. Avoid `isolation: isolate` on a parent of glass
 * cards — it forms a backdrop root and prevents backdrop-filter from sampling this layer.
 * Rasters: bundled under /assets via Vite (?url). Regenerate: scripts/generate-cyber-atmosphere.py.
 */
export function CyberBackdrop() {
  const tree = (
    <div
      className="pointer-events-none fixed inset-0 z-0 overflow-hidden"
      aria-hidden
      data-cyber-backdrop
    >
      <div className="cyber-bg-base absolute inset-0" />
      <img
        src={SRC_LIGHT}
        alt=""
        decoding="async"
        fetchPriority="low"
        className="cyber-bg-photo cyber-bg-photo-light absolute inset-0 h-full w-full object-cover select-none"
        {...imgDebugHandlers('light', SRC_LIGHT)}
      />
      <img
        src={SRC_DARK}
        alt=""
        decoding="async"
        fetchPriority="low"
        className="cyber-bg-photo cyber-bg-photo-dark absolute inset-0 h-full w-full object-cover select-none"
        {...imgDebugHandlers('dark', SRC_DARK)}
      />
      <div className="cyber-bg-aurora absolute inset-0" />
      <div className="cyber-bg-grid absolute inset-0" />
      <div className="cyber-bg-vignette absolute inset-0" />
      <div className="cyber-bg-scan absolute inset-0" />
    </div>
  )

  return createPortal(tree, document.body)
}
