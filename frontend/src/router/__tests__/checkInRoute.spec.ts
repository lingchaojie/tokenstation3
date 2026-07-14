import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const routerSource = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), '../index.ts'), 'utf8')

describe('daily check-in user route', () => {
  it('registers the authenticated ordinary-user check-in page', () => {
    const route = routerSource.match(/path: '\/check-in',[\s\S]*?\n  \},/)?.[0]

    expect(route).toContain("name: 'DailyCheckIn'")
    expect(route).toContain("import('@/views/user/CheckInView.vue')")
    expect(route).toContain('requiresAuth: true')
    expect(route).toContain('requiresAdmin: false')
  })

  it('registers check-in configuration under the admin affiliates route namespace', () => {
    const route = routerSource.match(/path: '\/admin\/affiliates\/check-in',[\s\S]*?\n  \},/)?.[0]

    expect(route).toContain("name: 'AdminDailyCheckInConfig'")
    expect(route).toContain("import('@/views/admin/affiliates/AdminCheckInConfigView.vue')")
    expect(route).toContain('requiresAdmin: true')
  })
})
