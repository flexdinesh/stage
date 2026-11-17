'use client'
import { useSyncExternalStore } from 'react'

export type SiteTheme = 'light' | 'dark'

function subscribe(onChange: () => void) {
  const mo = new MutationObserver(onChange)
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-vocs-theme'] })
  return () => mo.disconnect()
}

function read(): SiteTheme {
  return document.documentElement.getAttribute('data-vocs-theme') === 'dark' ? 'dark' : 'light'
}

/** The vocs color scheme, live. Pass it to `<Stage theme>` so demos follow the site toggle. */
export function useSiteTheme(): SiteTheme {
  return useSyncExternalStore(subscribe, read, () => 'light')
}
