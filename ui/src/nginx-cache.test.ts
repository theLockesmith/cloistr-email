import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// index.html must not be cached (it names the current bundles), but the hashed
// bundles under /assets/ must be: a no-store on `location /` alone also covered
// /assets/*, so every visit re-downloaded the whole app.
const conf = readFileSync(join(__dirname, '..', 'nginx.conf.template'), 'utf8')

function block(prefix: string): string {
  const start = conf.indexOf(`location ${prefix} {`)
  if (start < 0) return ''
  let depth = 0
  for (let i = conf.indexOf('{', start); i < conf.length; i++) {
    if (conf[i] === '{') depth++
    if (conf[i] === '}' && --depth === 0) return conf.slice(start, i + 1)
  }
  return ''
}

describe('nginx cache headers', () => {
  it('hashed assets have their own location, cached long and immutable', () => {
    const assets = block('/assets/')
    expect(assets, 'no `location /assets/` block').not.toBe('')
    expect(assets).toMatch(/Cache-Control "public, max-age=31536000, immutable" always/)
    expect(assets).not.toMatch(/no-store/)
    expect(assets, 'a missing asset must 404, not fall back to index.html').toMatch(/try_files \$uri =404;/)
  })

  it('index.html and the SPA fallback stay uncached', () => {
    const root = block('/')
    expect(root).toMatch(/no-store/)
    expect(root).toMatch(/try_files \$uri \$uri\/ \/index\.html;/)
  })
})
