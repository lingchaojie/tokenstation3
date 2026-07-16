interface LaCollectConfig {
  id: string
  ck: string
  d?: HTMLScriptElement
}

interface LaCollectQueue {
  id?: string
  ck?: string
  d?: HTMLScriptElement
  ids?: LaCollectConfig[]
}

type LaCollectWindow = Window & {
  LA?: LaCollectQueue
}

export const LA_SDK_SRC = 'https://sdk.51.la/js-sdk-pro.min.js'
export const LA_COLLECT_CONFIG = {
  id: '3QEWeLJeam88CaLO',
  ck: '3QEWeLJeam88CaLO'
} as const

const LA_COLLECT_SCRIPT_ID = 'LA_COLLECT'
const OFFICIAL_HOSTNAMES = new Set(['linx2.ai', 'www.linx2.ai', 'yundu.linx2.ai'])

export interface ShouldEnable51laAnalyticsOptions {
  hostname?: string
  isProduction?: boolean
}

export interface Init51laAnalyticsOptions extends ShouldEnable51laAnalyticsOptions {
  document?: Document
  window?: LaCollectWindow
}

function getRuntimeWindow(): LaCollectWindow | undefined {
  return typeof window === 'undefined' ? undefined : window
}

function getRuntimeDocument(): Document | undefined {
  return typeof document === 'undefined' ? undefined : document
}

function getRuntimeHostname(): string {
  return getRuntimeWindow()?.location.hostname ?? ''
}

export function shouldEnable51laAnalytics(
  options: ShouldEnable51laAnalyticsOptions = {}
): boolean {
  const isProduction = options.isProduction ?? import.meta.env.PROD
  const hostname = options.hostname ?? getRuntimeHostname()

  return isProduction && OFFICIAL_HOSTNAMES.has(hostname)
}

export function init51laAnalytics(options: Init51laAnalyticsOptions = {}): void {
  const runtimeWindow = options.window ?? getRuntimeWindow()
  const runtimeDocument = options.document ?? getRuntimeDocument()

  if (!runtimeWindow || !runtimeDocument) {
    return
  }

  const hostname = options.hostname ?? runtimeWindow.location.hostname
  const isProduction = options.isProduction ?? import.meta.env.PROD

  if (!shouldEnable51laAnalytics({ hostname, isProduction })) {
    return
  }

  if (runtimeDocument.querySelector(`script#${LA_COLLECT_SCRIPT_ID}`)) {
    return
  }

  const script = runtimeDocument.createElement('script')
  script.type = 'text/javascript'
  script.setAttribute('charset', 'UTF-8')
  script.async = true
  script.src = LA_SDK_SRC
  script.id = LA_COLLECT_SCRIPT_ID

  const config: LaCollectConfig = {
    id: LA_COLLECT_CONFIG.id,
    ck: LA_COLLECT_CONFIG.ck,
    d: script
  }

  if (runtimeWindow.LA?.ids) {
    runtimeWindow.LA.ids.push(config)
  } else {
    runtimeWindow.LA = {
      ...config,
      ids: [config]
    }
  }

  const firstScript = runtimeDocument.getElementsByTagName('script')[0]
  if (firstScript?.parentNode) {
    firstScript.parentNode.insertBefore(script, firstScript)
    return
  }

  runtimeDocument.head.appendChild(script)
}
