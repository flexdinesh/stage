'use client'
import { useEffect, useSyncExternalStore, type ReactNode } from 'react'

// Same storage key and DOM contract as vocs' own toggle (docs sidebar), so the
// two stay in sync: `data-vocs-theme` + `color-scheme` on <html>.
const storageKey = 'vocs-theme'
const changeEvent = 'site-theme-change'

type Theme = 'system' | 'light' | 'dark'

const themes: { value: Theme; label: string; icon: ReactNode }[] = [
  {
    value: 'system',
    label: 'System theme',
    icon: (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="M2.5 3.5h11v7h-11zM6 13.5h4M8 10.5v3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="square" />
      </svg>
    ),
  },
  {
    value: 'light',
    label: 'Light theme',
    icon: (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="M5.5 5.5h5v5h-5zM8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="square" />
      </svg>
    ),
  },
  {
    value: 'dark',
    label: 'Dark theme',
    icon: (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="M13.5 9.5A5.5 5.5 0 0 1 6.5 2.5a5.5 5.5 0 1 0 7 7z" stroke="currentColor" strokeWidth="1.5" strokeLinecap="square" strokeLinejoin="miter" />
      </svg>
    ),
  },
]

function isTheme(value: string | null): value is Theme {
  return value === 'system' || value === 'light' || value === 'dark'
}

function readStored(): Theme {
  try {
    const stored = localStorage.getItem(storageKey)
    return isTheme(stored) ? stored : 'system'
  } catch {
    return 'system'
  }
}

function apply(theme: Theme) {
  const resolved = theme === 'system' ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : theme
  const html = document.documentElement
  html.setAttribute('data-vocs-theme', resolved)
  html.style.colorScheme = resolved
}

function subscribe(onChange: () => void) {
  window.addEventListener(changeEvent, onChange)
  window.addEventListener('storage', onChange)
  return () => {
    window.removeEventListener(changeEvent, onChange)
    window.removeEventListener('storage', onChange)
  }
}

export function ThemeToggle() {
  const theme = useSyncExternalStore<Theme>(subscribe, readStored, () => 'system')

  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => {
      if (readStored() === 'system') apply('system')
    }
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])

  function choose(next: Theme) {
    try {
      localStorage.setItem(storageKey, next)
    } catch {
      // Storage unavailable: the choice still applies for this page view.
    }
    apply(next)
    window.dispatchEvent(new Event(changeEvent))
  }

  return (
    <div className="site-theme" role="radiogroup" aria-label="Theme">
      {themes.map((t) => (
        <button
          key={t.value}
          type="button"
          role="radio"
          aria-checked={theme === t.value}
          aria-label={t.label}
          title={t.label}
          className="site-theme__option"
          onClick={() => choose(t.value)}
        >
          {t.icon}
        </button>
      ))}
    </div>
  )
}
