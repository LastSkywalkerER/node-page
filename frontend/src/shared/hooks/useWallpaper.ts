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

/**
 * Returns the wallpaper to show right now, already preloaded (the swap waits
 * for the image bytes so the backdrop never flashes empty). Null = keep the
 * bundled static background.
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

  const [shown, setShown] = useState<WallpaperData | null>(null)
  const url = data?.url ?? null
  useEffect(() => {
    if (!isAuthenticated || !url || !data) {
      setShown(null)
      return
    }
    let cancelled = false
    const img = new Image()
    img.onload = () => {
      if (!cancelled) setShown(data)
    }
    img.src = url
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url, isAuthenticated])

  return shown
}
