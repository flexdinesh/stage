// Run the site, then: node scripts/generate-og.mjs [http://localhost:5175]
// Captures the stageshow needs-input frame and site glyph, with no duplicated logo.
import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const session = `stage-website-og-${process.pid}`
const run = (...args) => execFileSync('agent-browser', ['--session', session, ...args], { encoding: 'utf8', timeout: 60000 })
const url = process.argv[2] ?? 'http://localhost:5175'
const output = fileURLToPath(new URL('../public/og.png', import.meta.url))

try {
  run('set', 'viewport', '1200', '630', '1')
  run('set', 'media', 'light')
  run('open', url)
  run('wait', '--fn', "document.querySelector('.hdb .ss-stage')?.dataset.mark === 'answer'")
  const compose = async () => {
    await document.fonts.ready
    const title = document.querySelector('#hero-title')?.textContent
    const meta = document.querySelector('.lp-meta')?.cloneNode(true)
    const wordmark = document.querySelector('[data-v-logo-text]')?.cloneNode(true)
    const demo = document.querySelector('.hdb')?.cloneNode(true)
    if (!title || !meta || !wordmark || !demo) throw new Error('Landing content missing')
    document.documentElement.dataset.vocsTheme = 'light'
    const canvas = document.createElement('div')
    canvas.className = 'look-stage'
    canvas.style.cssText = 'position:fixed;inset:0;overflow:hidden;background:var(--st-bg);color:var(--st-fg);font-family:var(--st-font)'
    wordmark.style.cssText = 'position:absolute;left:24px;top:18px;font-size:18px;font-weight:700'
    const heading = document.createElement('div')
    heading.textContent = title
    heading.style.cssText = 'position:absolute;left:24px;top:184px;font-size:52px;font-weight:700;line-height:1.1;letter-spacing:-.035em;white-space:nowrap'
    meta.style.cssText = 'position:absolute;left:24px;top:260px;margin:0;font-size:12px'
    const holder = document.createElement('div')
    holder.style.cssText = 'position:absolute;left:24px;top:342px;width:1152px'
    demo.style.cssText = 'width:1152px;height:634px'
    const stage = demo.querySelector('.ss-stage')
    if (!stage) throw new Error('Stageshow frame missing')
    demo.querySelectorAll('.ss-cursor').forEach((cursor) => cursor.remove())
    stage.style.transform = `scale(${1152 / 1240})`
    holder.append(demo)
    canvas.append(wordmark, heading, meta, holder)
    document.body.replaceChildren(canvas)
    await new Promise(requestAnimationFrame)
    return { title, width: innerWidth, height: innerHeight }
  }
  console.log(run('eval', '-b', Buffer.from(`(${compose.toString()})()`).toString('base64')).trim())
  run('screenshot', output)
  console.log(output)
} finally {
  run('close')
}
