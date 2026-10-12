import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { listPlatformIds } from '@/constants/platformCatalog'

describe('channel platform options', () => {
  it('includes all concrete providers without restoring Composite routing', () => {
    const source = readFileSync(resolve('src/views/admin/ChannelsView.vue'), 'utf8')
    expect(source).toMatch(/const platformOrder = computed<GroupPlatform\[\]>\(\(\) => listPlatformIds\(\)\)/)
    expect(listPlatformIds()).toEqual(
      expect.arrayContaining(['kiro', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe', 'command_code', 'cline'])
    )
    expect(source).not.toMatch(/compositePlatforms|g\.platform === 'composite'/)
  })
})
