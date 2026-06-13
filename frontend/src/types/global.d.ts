import type { PublicSettings } from '@/types'

declare global {
  interface Window {
    __APP_CONFIG__?: PublicSettings
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
}

export {}
