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
  it('renders the chat route as an immersive workspace instead of the app shell', () => {
    const source = read(resolve(userDir, 'ChatView.vue'))

    expect(source).toContain('data-testid="chat-immersive-view"')
    expect(source).toContain('<ChatShell')
    expect(source).not.toContain('<AppLayout>')
    expect(source).not.toContain('Get started')
  })

  it('uses a full-height chat grid instead of a marketing or card-heavy page shell', () => {
    const source = read(resolve(chatDir, 'ChatShell.vue'))

    expect(source).toContain('chat-page')
    expect(source).toContain('h-[100dvh]')
    expect(source).toContain('lg:grid-cols-[292px_minmax(0,1fr)]')
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
