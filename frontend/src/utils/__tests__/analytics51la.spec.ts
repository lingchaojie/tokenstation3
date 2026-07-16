import { afterEach, describe, expect, it } from 'vitest'

import {
  init51laAnalytics,
  LA_COLLECT_CONFIG,
  LA_SDK_SRC,
  shouldEnable51laAnalytics
} from '../analytics51la'

type TestLaCollectWindow = Window &
  typeof globalThis & {
    LA?: {
      id?: string
      ck?: string
      d?: HTMLScriptElement
      ids?: Array<{
        id: string
        ck: string
        d?: HTMLScriptElement
        autoTrack?: boolean
        hashMode?: boolean
        screenRecord?: boolean
      }>
    }
  }

const analyticsWindow = window as TestLaCollectWindow

function resetAnalyticsDom(): void {
  document.head.innerHTML = ''
  document.body.innerHTML = ''
  delete analyticsWindow.LA
}

afterEach(() => {
  resetAnalyticsDom()
})

describe('shouldEnable51laAnalytics', () => {
  it('enables analytics only for production builds on official LINX2 domains', () => {
    expect(shouldEnable51laAnalytics({ isProduction: true, hostname: 'linx2.ai' })).toBe(true)
    expect(shouldEnable51laAnalytics({ isProduction: true, hostname: 'www.linx2.ai' })).toBe(true)
    expect(shouldEnable51laAnalytics({ isProduction: true, hostname: 'yundu.linx2.ai' })).toBe(true)
    expect(shouldEnable51laAnalytics({ isProduction: false, hostname: 'linx2.ai' })).toBe(false)
    expect(shouldEnable51laAnalytics({ isProduction: true, hostname: 'localhost' })).toBe(false)
    expect(shouldEnable51laAnalytics({ isProduction: true, hostname: 'preview.linx2.ai' })).toBe(false)
    expect(shouldEnable51laAnalytics({ isProduction: true, hostname: 'other.linx2.ai' })).toBe(false)
    expect(shouldEnable51laAnalytics({ isProduction: true, hostname: 'linx2.com' })).toBe(false)
  })
})

describe('init51laAnalytics', () => {
  it('does not inject the SDK in non-production mode', () => {
    init51laAnalytics({
      isProduction: false,
      hostname: 'linx2.ai',
      window,
      document
    })

    expect(document.getElementById('LA_COLLECT')).toBeNull()
    expect(analyticsWindow.LA).toBeUndefined()
  })

  it('does not inject the SDK on non-official production hosts', () => {
    init51laAnalytics({
      isProduction: true,
      hostname: 'staging.linx2.ai',
      window,
      document
    })

    expect(document.getElementById('LA_COLLECT')).toBeNull()
    expect(analyticsWindow.LA).toBeUndefined()
  })

  it('injects the 51.LA SDK before the first existing body script on linx2.ai production', () => {
    const appRoot = document.createElement('div')
    appRoot.id = 'app'
    document.body.appendChild(appRoot)

    const appScript = document.createElement('script')
    appScript.id = 'app-entry'
    appScript.type = 'module'
    appScript.src = '/src/main.ts'
    document.body.appendChild(appScript)

    init51laAnalytics({
      isProduction: true,
      hostname: 'linx2.ai',
      window,
      document
    })

    const sdkScript = document.getElementById('LA_COLLECT') as HTMLScriptElement | null

    expect(sdkScript).not.toBeNull()
    expect(sdkScript?.tagName).toBe('SCRIPT')
    expect(sdkScript?.src).toBe(LA_SDK_SRC)
    expect(sdkScript?.id).toBe('LA_COLLECT')
    expect(sdkScript?.getAttribute('charset')).toBe('UTF-8')
    expect(sdkScript?.async).toBe(true)
    expect(sdkScript?.parentElement).toBe(document.body)
    expect(sdkScript?.nextElementSibling).toBe(appScript)
    expect(appScript.previousElementSibling).toBe(sdkScript)
    expect(analyticsWindow.LA?.ids).toHaveLength(1)
    expect(analyticsWindow.LA?.ids?.[0]).toMatchObject(LA_COLLECT_CONFIG)
    expect(analyticsWindow.LA?.ids?.[0]?.d).toBe(sdkScript)
    expect(analyticsWindow.LA?.ids?.[0]).not.toHaveProperty('autoTrack')
    expect(analyticsWindow.LA?.ids?.[0]).not.toHaveProperty('hashMode')
    expect(analyticsWindow.LA?.ids?.[0]).not.toHaveProperty('screenRecord')
  })

  it('injects the 51.LA SDK on www.linx2.ai production', () => {
    init51laAnalytics({
      isProduction: true,
      hostname: 'www.linx2.ai',
      window,
      document
    })

    expect(document.getElementById('LA_COLLECT')).not.toBeNull()
    expect(analyticsWindow.LA?.ids).toHaveLength(1)
  })

  it('injects the 51.LA SDK on yundu.linx2.ai production', () => {
    init51laAnalytics({
      isProduction: true,
      hostname: 'yundu.linx2.ai',
      window,
      document
    })

    expect(document.getElementById('LA_COLLECT')).not.toBeNull()
    expect(analyticsWindow.LA?.ids).toHaveLength(1)
    expect(analyticsWindow.LA?.ids?.[0]).toMatchObject(LA_COLLECT_CONFIG)
  })

  it('does not inject duplicate SDK scripts when called more than once', () => {
    init51laAnalytics({
      isProduction: true,
      hostname: 'linx2.ai',
      window,
      document
    })
    init51laAnalytics({
      isProduction: true,
      hostname: 'linx2.ai',
      window,
      document
    })

    expect(document.querySelectorAll('script#LA_COLLECT')).toHaveLength(1)
    expect(analyticsWindow.LA?.ids).toHaveLength(1)
  })

  it('leaves the page untouched when an SDK script already exists', () => {
    const existingScript = document.createElement('script')
    existingScript.id = 'LA_COLLECT'
    existingScript.src = 'https://sdk.51.la/js-sdk-pro.min.js?id=existing'
    existingScript.setAttribute('charset', 'ISO-8859-1')
    existingScript.setAttribute('data-sentinel', 'keep-me')
    existingScript.textContent = 'window.__existing51laSentinel = true'
    document.head.appendChild(existingScript)

    init51laAnalytics({
      isProduction: true,
      hostname: 'linx2.ai',
      window,
      document
    })

    expect(document.querySelectorAll('script#LA_COLLECT')).toHaveLength(1)
    expect(document.getElementById('LA_COLLECT')).toBe(existingScript)
    expect(existingScript.src).toBe('https://sdk.51.la/js-sdk-pro.min.js?id=existing')
    expect(existingScript.getAttribute('charset')).toBe('ISO-8859-1')
    expect(existingScript.getAttribute('data-sentinel')).toBe('keep-me')
    expect(existingScript.textContent).toBe('window.__existing51laSentinel = true')
    expect(analyticsWindow.LA).toBeUndefined()
  })

  it('injects the SDK when a non-script element uses the LA_COLLECT id', () => {
    const nonScriptElement = document.createElement('div')
    nonScriptElement.id = 'LA_COLLECT'
    document.body.appendChild(nonScriptElement)

    init51laAnalytics({
      isProduction: true,
      hostname: 'linx2.ai',
      window,
      document
    })

    const sdkScript = document.querySelector('script#LA_COLLECT') as HTMLScriptElement | null

    expect(sdkScript).not.toBeNull()
    expect(sdkScript?.src).toBe(LA_SDK_SRC)
    expect(document.querySelectorAll('script#LA_COLLECT')).toHaveLength(1)
    expect(document.getElementById('LA_COLLECT')).toBe(nonScriptElement)
    expect(analyticsWindow.LA?.ids).toHaveLength(1)
    expect(analyticsWindow.LA?.ids?.[0]).toMatchObject(LA_COLLECT_CONFIG)
    expect(analyticsWindow.LA?.ids?.[0]?.d).toBe(sdkScript)
  })

  it('reuses an existing 51.LA queue when present', () => {
    const existingQueuedEntry = { id: 'already-queued', ck: 'already-queued' }
    const existingIds = [existingQueuedEntry]
    const existingQueue = { ids: existingIds }
    analyticsWindow.LA = existingQueue

    init51laAnalytics({
      isProduction: true,
      hostname: 'linx2.ai',
      window,
      document
    })

    expect(analyticsWindow.LA).toBe(existingQueue)
    expect(analyticsWindow.LA.ids).toBe(existingIds)
    expect(analyticsWindow.LA.ids).toHaveLength(2)
    expect(analyticsWindow.LA.ids?.[0]).toBe(existingQueuedEntry)
    expect(analyticsWindow.LA.ids?.[1]).toMatchObject(LA_COLLECT_CONFIG)
  })
})
