// Records the README cover: one loop of ReadmeCover (src/components/demos/
// readme-cover), stepped frame by frame at 60 fps on the kit's virtual clock,
// captured at 2x and encoded as an animated WebP per theme, plus the loop's
// frame at the scene's `poster` mark as a PNG. Run `mise run readme:cover`;
// `-- --keep` keeps the frames for review, `-- light` or `-- dark` records one
// theme. See docs/development.md (README cover).

import { execFile } from 'node:child_process'
import { copyFile, mkdir, mkdtemp, rm, stat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { promisify } from 'node:util'
import { chromium, type Browser, type Page } from '@playwright/test'
import { createServer } from 'vite'

const FPS = 60
// A loop longer than this means the scene never reached its loop mark.
const MAX_FRAMES = FPS * 40
// Lossless first; a theme larger than this is re-encoded lossy.
const MAX_BYTES = 8 * 1024 * 1024

type Theme = 'light' | 'dark'

const here = import.meta.dirname
const out = path.resolve(here, '../../../docs/assets')
const run = promisify(execFile)
const args = process.argv.slice(2)
const keep = args.includes('--keep')
const themes: Theme[] = args.includes('light') ? ['light'] : args.includes('dark') ? ['dark'] : ['light', 'dark']

async function step(page: Page): Promise<string | null> {
  return page.evaluate(async () => {
    const cover = window.__cover
    if (cover === undefined) throw new Error('cover: not in record mode')
    return cover.step()
  })
}

async function recordLoop(browser: Browser, url: string, theme: Theme) {
  const context = await browser.newContext({ viewport: { width: 920, height: 420 }, deviceScaleFactor: 2, colorScheme: theme })
  const page = await context.newPage()
  await page.goto(`${url}?record=${FPS}&theme=${theme}`)
  const frame = page.locator('.cover-frame')
  await frame.waitFor()
  await page.waitForFunction(() => window.__cover !== undefined)
  await page.evaluate(async () => {
    await document.fonts.ready
  })

  // The first loop warms up (the cursor learns its first move); the second,
  // from the frame after the first loop mark up to the next, is recorded, so
  // its last frame leads straight into its first.
  let warm = 0
  while ((await step(page)) !== 'loop') {
    if (++warm > MAX_FRAMES) throw new Error(`cover: ${theme} warm-up never reached its loop mark`)
  }

  const dir = await mkdtemp(path.join(tmpdir(), `stage-cover-${theme}-`))
  let cleared = false
  let poster = false
  let count = 0
  for (;;) {
    const mark = await step(page)
    if (mark !== 'loop') cleared = true
    else if (cleared) break
    if (count >= MAX_FRAMES) throw new Error(`cover: ${theme} loop never reached its loop mark`)
    const shot = path.join(dir, `f${String(count).padStart(4, '0')}.png`)
    const png = await frame.screenshot({ path: shot, omitBackground: true })
    // PNG color type 6 is RGBA: anything else means the page painted a ground
    if (count === 0 && png[25] !== 6) throw new Error(`cover: ${theme} frames have no alpha; something paints behind the scene`)
    if (mark === 'poster' && !poster) {
      await copyFile(shot, path.join(out, `readme-cover-${theme}.png`))
      poster = true
    }
    count++
  }
  await context.close()
  if (!poster) throw new Error(`cover: ${theme} loop never reached its poster mark`)
  return { dir, count }
}

async function encode(dir: string, theme: Theme): Promise<{ target: string; lossless: boolean; bytes: number }> {
  const target = path.join(out, `readme-cover-${theme}.webp`)
  const input = ['-y', '-loglevel', 'error', '-framerate', String(FPS), '-i', path.join(dir, 'f%04d.png')]
  // Frames are already 2x (1840 px wide): no scaling, so text stays sharp.
  await run('ffmpeg', [...input, '-c:v', 'libwebp_anim', '-lossless', '1', '-quality', '75', '-compression_level', '4', '-pix_fmt', 'bgra', '-loop', '0', target], { maxBuffer: 1 << 26 })
  const lossless = (await stat(target)).size
  if (lossless <= MAX_BYTES) return { target, lossless: true, bytes: lossless }
  await run('ffmpeg', [...input, '-c:v', 'libwebp_anim', '-lossless', '0', '-quality', '92', '-preset', 'text', '-compression_level', '6', '-pix_fmt', 'yuva420p', '-loop', '0', target], { maxBuffer: 1 << 26 })
  return { target, lossless: false, bytes: (await stat(target)).size }
}

async function main() {
  await mkdir(out, { recursive: true })
  const server = await createServer({
    configFile: path.join(here, '../cover/vite.config.ts'),
    logLevel: 'error',
    // No reloads mid-take: an edited file must not restart the scene.
    server: { port: 0, hmr: false, watch: null },
  })
  await server.listen()
  const url = server.resolvedUrls?.local[0]
  if (url === undefined) throw new Error('cover: the capture server has no local URL')
  const browser = await chromium.launch()
  try {
    for (const theme of themes) {
      const { dir, count } = await recordLoop(browser, url, theme)
      const { target, lossless, bytes } = await encode(dir, theme)
      if (keep) console.log(`${theme}: frames kept in ${dir}`)
      else await rm(dir, { recursive: true, force: true })
      const mb = (bytes / 1024 / 1024).toFixed(2)
      console.log(`${theme}: ${count} frames (${(count / FPS).toFixed(2)} s at ${FPS} fps), ${lossless ? 'lossless' : 'lossy q92'}, ${mb} MB -> ${path.relative(process.cwd(), target)}`)
    }
  } finally {
    await browser.close()
    await server.close()
  }
}

await main()
