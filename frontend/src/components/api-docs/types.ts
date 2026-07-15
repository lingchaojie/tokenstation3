export type ApiDocsPageKind = 'guide' | 'endpoint' | 'platform'
export type ApiDocsMethod = 'GET' | 'POST'
export type ApiDocsProtocol = 'anthropic' | 'openai' | 'common'

export type ApiDocsPageId =
  | 'quickstart'
  | 'authentication'
  | 'client-integration'
  | 'capabilities'
  | 'messages'
  | 'count-tokens'
  | 'responses'
  | 'chat-completions'
  | 'models'
  | 'image-generations'
  | 'image-edits'
  | 'errors'
  | 'request-id'
  | 'key-security'

export interface ApiDocsPage {
  id: ApiDocsPageId
  kind: ApiDocsPageKind
  path: string
  titleKey: string
  summaryKey: string
  keywords: string[]
  endpointId?: ApiEndpointId
}

export interface ApiDocsNavGroup {
  id: 'quickstart' | 'clients' | 'reference' | 'advanced' | 'platform'
  labelKey: string
  pageIds: ApiDocsPageId[]
}

export interface ApiParameter {
  name: string
  location: 'body' | 'header' | 'path'
  required: boolean
  type: string
  descriptionKey: string
}

export type ApiEndpointId =
  | 'messages'
  | 'count-tokens'
  | 'responses'
  | 'chat-completions'
  | 'models'
  | 'image-generations'
  | 'image-edits'

export interface ApiEndpointDefinition {
  id: ApiEndpointId
  pageId: ApiDocsPageId
  method: ApiDocsMethod
  path: string
  protocol: ApiDocsProtocol
  titleKey: string
  summaryKey: string
  parameters: ApiParameter[]
  errorCodes: string[]
  supportsStreaming: boolean
}

export interface ApiEndpointExamples {
  curl: string
  python?: string
  success: string
  stream?: string
}

export type ApiDocsTableValue =
  | { kind: 'raw'; value: string }
  | { kind: 'localized'; textKey: string }

export type ApiDocsBlock =
  | { kind: 'paragraph'; textKey: string }
  | { kind: 'callout'; tone: 'info' | 'warning'; textKey: string }
  | { kind: 'code'; label: string; language: string; code: string }
  | { kind: 'table'; columns: ApiDocsTableValue[]; rows: ApiDocsTableValue[][] }
  | { kind: 'links'; links: Array<{ labelKey: string; to: string }> }

export interface ApiDocsGuideSection {
  id: string
  titleKey: string
  blocks: ApiDocsBlock[]
}

export interface ApiDocsGuideDefinition {
  pageId: ApiDocsPageId
  sections: ApiDocsGuideSection[]
}
