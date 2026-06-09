import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const userDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const commonDir = resolve(userDir, '..', '..', 'components/common')
const keysDir = resolve(userDir, '..', '..', 'components/keys')
const dataTableSource = readFileSync(resolve(commonDir, 'DataTable.vue'), 'utf8')
const paginationSource = readFileSync(resolve(commonDir, 'Pagination.vue'), 'utf8')
const emptyStateSource = readFileSync(resolve(commonDir, 'EmptyState.vue'), 'utf8')
const keysSource = readFileSync(resolve(userDir, 'KeysView.vue'), 'utf8')
const usageSource = readFileSync(resolve(userDir, 'UsageView.vue'), 'utf8')
const useKeyModalSource = readFileSync(resolve(keysDir, 'UseKeyModal.vue'), 'utf8')
const endpointPopoverSource = readFileSync(resolve(keysDir, 'EndpointPopover.vue'), 'utf8')

describe('Linear data page contract', () => {
  it('uses Linear table surfaces and keeps virtual scroll code intact', () => {
    expect(dataTableSource).toContain('linear-data-table')
    expect(dataTableSource).toContain('dark:bg-linear-surface-1')
    expect(dataTableSource).toContain('useVirtualizer')
    expect(dataTableSource).toContain('observeElementRectNonZero')
  })

  it('uses developer-console styling on Keys and Usage pages', () => {
    expect(keysSource).toContain('linear-keys-page')
    expect(keysSource).toContain('code text-xs tracking-[-0.01em]')
    expect(keysSource).not.toContain('aria-hidden="true" class="linx-code-panel hidden"')
    expect(usageSource).toContain('linear-usage-page')
    expect(usageSource).toContain('linx-panel')
  })

  it('updates supporting data components to Linear surfaces', () => {
    expect(paginationSource).toContain('border-linear-hairline')
    expect(emptyStateSource).toContain('linx-panel')
    expect(useKeyModalSource).toContain('linx-code-panel')
    expect(endpointPopoverSource).toContain('linx-panel')
  })
})
