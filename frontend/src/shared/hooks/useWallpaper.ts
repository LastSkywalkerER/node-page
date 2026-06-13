import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiClient } from '@/shared/lib/api'
import { useUserStore } from '@/shared/store/user'

/**
 * Dynamic wallpaper fed by the backend Pexels proxy (GET /wallpaper). The
 * server rotates the photo every 5 minutes on a wall-clock bucket; the client
 * just refetches on the same cadence. Disabled until signed in (the endpoint
 * is authenticated) — the bundled static art stays as the fallback.
 */
export interface WallpaperData {
  url: string
  photographer: string
  photographer_url: string
  source_url: string
  avg_color: string
  rotate_seconds: number
}

/** Tracks the html.dark class so the wallpaper follows theme toggles live. */
export function useDarkClass(): boolean {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  useEffect(() => {
    const obs = new MutationObserver(() =>
      setDark(document.documentElement.classList.contains('dark'))
    )
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => obs.disconnect()
  }, [])
  return dark
}

// Last-shown wallpaper is cached per theme so a returning visit (or a theme
// toggle) paints the Pexels photo IMMEDIATELY — the photo URL is stable and
// almost certainly still in the browser's HTTP cache — instead of flashing the
// bundled art for a beat while the /wallpaper fetch + image preload run.
const WP_CACHE = 'np:wallpaper:'

function readWallpaperCache(mode: string): WallpaperData | null {
  try {
    const raw = localStorage.getItem(WP_CACHE + mode)
    if (!raw) return null
    const v = JSON.parse(raw)
    return v && typeof v.url === 'string' ? (v as WallpaperData) : null
  } catch {
    return null
  }
}

function writeWallpaperCache(mode: string, data: WallpaperData | null) {
  try {
    if (data) localStorage.setItem(WP_CACHE + mode, JSON.stringify(data))
    else localStorage.removeItem(WP_CACHE + mode)
  } catch {
    /* ignore quota / private mode */
  }
}

/**
 * Returns the wallpaper to show right now. Seeded synchronously from the
 * per-theme localStorage cache so the Pexels photo appears with no
 * bundled-art flash; the fresh fetch then preloads and swaps in the current
 * rotation. Null = no wallpaper (not configured / not signed in) → bundled art.
 */
export function useLoadedWallpaper(): WallpaperData | null {
  const isAuthenticated = useUserStore((s) => s.isAuthenticated)
  const dark = useDarkClass()
  const mode = dark ? 'dark' : 'light'

  const { data } = useQuery<WallpaperData | null>({
    queryKey: ['wallpaper', mode],
    queryFn: async () => {
      const res = await apiClient.get(`/wallpaper?mode=${mode}`)
      // 204 = no Pexels connector configured.
      if (res.status !== 200 || !res.data?.data?.url) return null
      return res.data.data as WallpaperData
    },
    enabled: isAuthenticated,
    refetchInterval: 5 * 60 * 1000,
    staleTime: 60 * 1000,
    retry: false,
  })

  const [shown, setShown] = useState<WallpaperData | null>(() =>
    isAuthenticated ? readWallpaperCache(mode) : null
  )

  // Re-seed instantly from the cache on theme flip / sign-in, so toggling never
  // momentarily drops back to the bundled art for an already-seen wallpaper.
  useEffect(() => {
    if (isAuthenticated) {
      const cached = readWallpaperCache(mode)
      if (cached) setShown(cached)
    } else {
      setShown(null)
    }
  }, [mode, isAuthenticated])

  const url = data?.url ?? null
  useEffect(() => {
    if (!isAuthenticated) return // logout handled by the seed effect above
    if (data === null) {
      // Query resolved with NO connector — clear the photo and the stale cache.
      setShown(null)
      writeWallpaperCache(mode, null)
      return
    }
    if (!data || !url) return // still loading — keep whatever is shown (cached)
    let cancelled = false
    const img = new Image()
    img.onload = () => {
      if (!cancelled) {
        setShown(data)
        writeWallpaperCache(mode, data)
      }
    }
    img.src = url
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url, data, isAuthenticated, mode])

  return shown
}
