import { useState, useEffect } from 'react'

export type ColorMode = 'dark' | 'light'

export function initTheme(): void {
  const stored = localStorage.getItem('theme')
  applyTheme(stored === 'light' ? 'light' : 'dark')
}

export function applyTheme(mode: ColorMode): void {
  if (mode === 'dark') {
    document.documentElement.classList.add('dark')
  } else {
    document.documentElement.classList.remove('dark')
  }
  localStorage.setItem('theme', mode)
}

export function useTheme() {
  const [theme, setTheme] = useState<ColorMode>(() => {
    const stored = localStorage.getItem('theme')
    return stored === 'light' ? 'light' : 'dark'
  })

  useEffect(() => {
    applyTheme(theme)
  }, [theme])

  const toggle = () => setTheme(prev => (prev === 'dark' ? 'light' : 'dark'))

  return { theme, toggle }
}
