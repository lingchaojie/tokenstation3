import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const userDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const chatDir = resolve(userDir, '..', '..', 'components/chat')

function read(relativePath: string): string {
  return readFileSync(resolve(relativePath), 'utf8')
}

describe('Chat page source contract', () => {
  it('keeps the route view as an authenticated app-layout wrapper', () => {
    const source = read(resolve(userDir, 'ChatView.vue'))

    expect(source).toContain('<AppLayout>')
    expect(source).toContain('<ChatShell')
    expect(source).not.toContain('Get started')
  })

  it('uses a full-height chat grid instead of a marketing or card-heavy page shell', () => {
    const source = read(resolve(chatDir, 'ChatShell.vue'))

    expect(source).toContain('chat-page')
    expect(source).toContain('grid h-[calc(100vh-4rem)] min-h-[640px]')
    expect(source).toContain('lg:grid-cols-[280px_minmax(0,1fr)]')
    expect(source).not.toContain('linx-panel-strong')
    expect(source).not.toContain('hero')
  })

  it('keeps the composer controls addressable and keyboard-driven', () => {
    const source = read(resolve(chatDir, 'Composer.vue'))

    expect(source).toContain('data-testid="chat-send"')
    expect(source).toContain('data-testid="chat-stop"')
    expect(source).toContain('@keydown.enter')
  })
})
