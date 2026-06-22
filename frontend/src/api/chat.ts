import { apiClient } from './client'
import type { PaginatedResponse } from '@/types'

export type WebChatPriceStatus = 'confirmed' | 'unverified'
export type WebChatConversationStatus = 'active' | 'archived' | 'deleted'
export type WebChatMessageRole = 'user' | 'assistant' | 'system'
export type WebChatMessageStatus = 'pending' | 'streaming' | 'completed' | 'failed' | 'canceled'
export type WebChatAttachmentKind = 'image' | 'file'
export type WebChatAttachmentStatus = 'uploaded' | 'processed' | 'unsupported' | 'deleted'
export type WebChatArtifactSource = 'model_output' | 'image_output' | 'generated_file'

export interface WebChatModel {
  provider: string
  platform: string
  key_type: string
  model: string
  display_name: string
  supports_text: boolean
  supports_image_input: boolean
  supports_file_context: boolean
  supports_artifact_output: boolean
  price_status: WebChatPriceStatus
}

export interface WebChatConversation {
  id: number
  title: string
  default_model: string
  default_provider: string
  last_model: string
  last_provider: string
  status: WebChatConversationStatus
  message_count: number
  last_message_at?: string
  created_at: string
  updated_at: string
}

export interface WebChatAttachment {
  id: number
  message_id?: number
  conversation_id?: number
  user_id: number
  kind: WebChatAttachmentKind
  filename: string
  content_type: string
  size_bytes: number
  storage_key: string
  sha256: string
  text_preview?: string
  status: WebChatAttachmentStatus
  created_at: string
}

export interface WebChatArtifact {
  id: number
  message_id: number
  conversation_id: number
  user_id: number
  filename: string
  content_type: string
  size_bytes: number
  storage_key: string
  sha256: string
  source: WebChatArtifactSource
  created_at: string
}

export interface WebChatMessage {
  id: number
  conversation_id: number
  user_id: number
  role: WebChatMessageRole
  model: string
  provider: string
  content_text: string
  content_json: Array<Record<string, unknown>>
  status: WebChatMessageStatus
  error_code?: string
  error_message?: string
  usage_log_id?: number
  created_at: string
  updated_at: string
  attachments?: WebChatAttachment[]
  artifacts?: WebChatArtifact[]
}

export interface WebChatConversationDetail {
  conversation: WebChatConversation
  messages: WebChatMessage[]
}

export interface CreateWebChatConversationRequest {
  model: string
  provider: string
  title?: string
}

export interface UpdateWebChatConversationRequest {
  model?: string
  provider?: string
  title?: string
  status?: WebChatConversationStatus
}

export interface SendWebChatMessageRequest {
  model: string
  provider: string
  content: string
  attachment_ids?: number[]
  stream?: boolean
}

export interface WebChatStreamSendResult {
  response: Response
  userMessageId: number | null
  assistantMessageId: number | null
}

export interface WebChatDownload {
  blob: Blob
  filename: string
  contentType: string
}

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1'

function parseNumericHeader(response: Response, name: string): number | null {
  const value = response.headers.get(name)
  if (!value) return null
  const parsed = Number.parseInt(value, 10)
  return Number.isFinite(parsed) ? parsed : null
}

function chatDownloadUrl(path: string): string {
  return `${API_BASE_URL}${path}`
}

function getHeader(headers: unknown, name: string): string {
  const lowerName = name.toLowerCase()
  if (headers instanceof Headers) {
    return headers.get(name) || ''
  }
  if (headers && typeof (headers as { get?: unknown }).get === 'function') {
    return String((headers as { get: (key: string) => unknown }).get(name) || '')
  }
  if (headers && typeof headers === 'object') {
    const record = headers as Record<string, unknown>
    const value = record[name] ?? record[lowerName]
    return typeof value === 'string' ? value : ''
  }
  return ''
}

function parseDownloadFilename(contentDisposition: string, fallback: string): string {
  const encodedMatch = contentDisposition.match(/filename\*=UTF-8''([^;]+)/i)
  if (encodedMatch?.[1]) {
    try {
      return decodeURIComponent(encodedMatch[1].trim())
    } catch {
      return encodedMatch[1].trim()
    }
  }

  const filenameMatch = contentDisposition.match(/filename="?([^";]+)"?/i)
  return filenameMatch?.[1]?.trim() || fallback
}

async function buildStreamError(response: Response): Promise<Error> {
  let body = ''
  try {
    body = await response.text()
  } catch {
    body = ''
  }

  if (body) {
    try {
      const parsed = JSON.parse(body) as { message?: string; reason?: string; detail?: string }
      const message = parsed.message || parsed.reason || parsed.detail
      if (message) return new Error(message)
    } catch {
      // fall back to the raw response body below
    }
    return new Error(body)
  }

  return new Error(`Chat stream request failed with status ${response.status}`)
}

function clearExpiredAuth(redirect = true): void {
  localStorage.removeItem('auth_token')
  localStorage.removeItem('refresh_token')
  localStorage.removeItem('auth_user')
  localStorage.removeItem('token_expires_at')
  sessionStorage.setItem('auth_expired', '1')

  if (redirect && !window.location.pathname.includes('/login')) {
    window.location.href = '/login'
  }
}

async function refreshStreamAuthToken(): Promise<string> {
  const refreshToken = localStorage.getItem('refresh_token')
  if (!refreshToken) {
    throw new Error('Missing refresh token')
  }

  const response = await fetch(`${API_BASE_URL}/auth/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refresh_token: refreshToken }),
  })

  if (!response.ok) {
    throw new Error(`Token refresh failed with status ${response.status}`)
  }

  const payload = await response.json() as {
    code?: number
    data?: {
      access_token?: string
      refresh_token?: string
      expires_in?: number
    }
    message?: string
  }
  const accessToken = payload.data?.access_token
  const newRefreshToken = payload.data?.refresh_token
  const expiresIn = payload.data?.expires_in
  if (payload.code !== 0 || !accessToken || !newRefreshToken || typeof expiresIn !== 'number') {
    throw new Error(payload.message || 'Token refresh failed')
  }

  localStorage.setItem('auth_token', accessToken)
  localStorage.setItem('refresh_token', newRefreshToken)
  localStorage.setItem('token_expires_at', String(Date.now() + expiresIn * 1000))
  return accessToken
}

function buildStreamHeaders(token: string | null): Record<string, string> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  return headers
}

function streamRequestInit(request: SendWebChatMessageRequest, token: string | null, signal?: AbortSignal): RequestInit {
  return {
    method: 'POST',
    headers: buildStreamHeaders(token),
    body: JSON.stringify({ ...request, stream: true }),
    signal,
  }
}

export async function listChatModels(): Promise<WebChatModel[]> {
  const { data } = await apiClient.get<WebChatModel[]>('/chat/models')
  return data ?? []
}

export async function listChatConversations(
  page = 1,
  pageSize = 50
): Promise<PaginatedResponse<WebChatConversation>> {
  const { data } = await apiClient.get<PaginatedResponse<WebChatConversation>>('/chat/conversations', {
    params: { page, page_size: pageSize },
  })
  return data
}

export async function getChatConversation(id: number): Promise<WebChatConversationDetail> {
  const { data } = await apiClient.get<WebChatConversationDetail>(`/chat/conversations/${id}`)
  return data
}

export async function createChatConversation(
  request: CreateWebChatConversationRequest
): Promise<WebChatConversation> {
  const { data } = await apiClient.post<WebChatConversation>('/chat/conversations', request)
  return data
}

export async function updateChatConversation(
  id: number,
  request: UpdateWebChatConversationRequest
): Promise<WebChatConversation> {
  const { data } = await apiClient.patch<WebChatConversation>(`/chat/conversations/${id}`, request)
  return data
}

export async function deleteChatConversation(id: number): Promise<void> {
  await apiClient.delete(`/chat/conversations/${id}`)
}

export async function uploadChatAttachment(file: File): Promise<WebChatAttachment> {
  const formData = new FormData()
  formData.append('file', file)
  const { data } = await apiClient.post<WebChatAttachment>('/chat/attachments', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
  return data
}

export async function cancelChatMessage(conversationId: number, messageId: number): Promise<void> {
  await apiClient.post(`/chat/conversations/${conversationId}/messages/${messageId}/cancel`)
}

export async function sendChatMessageStream(
  conversationId: number,
  request: SendWebChatMessageRequest,
  signal?: AbortSignal
): Promise<WebChatStreamSendResult> {
  const url = `${API_BASE_URL}/chat/conversations/${conversationId}/messages`
  let response = await fetch(url, streamRequestInit(request, localStorage.getItem('auth_token'), signal))

  if (response.status === 401 && localStorage.getItem('refresh_token')) {
    try {
      const token = await refreshStreamAuthToken()
      response = await fetch(url, streamRequestInit(request, token, signal))
    } catch {
      clearExpiredAuth()
      throw new Error('Session expired. Please log in again.')
    }
  }

  if (!response.ok) {
    if (response.status === 401) {
      clearExpiredAuth(!!localStorage.getItem('auth_token'))
    }
    throw await buildStreamError(response)
  }
  if (!response.body) {
    throw new Error(`Chat stream response did not include a readable body (status ${response.status})`)
  }

  return {
    response,
    userMessageId: parseNumericHeader(response, 'X-Web-Chat-User-Message-ID'),
    assistantMessageId: parseNumericHeader(response, 'X-Web-Chat-Assistant-Message-ID'),
  }
}

export function chatAttachmentDownloadUrl(id: number): string {
  return chatDownloadUrl(`/chat/attachments/${id}/download`)
}

export function chatArtifactDownloadUrl(id: number): string {
  return chatDownloadUrl(`/chat/artifacts/${id}/download`)
}

async function downloadChatBlob(path: string, fallbackName: string): Promise<WebChatDownload> {
  const response = await apiClient.get<Blob>(path, { responseType: 'blob' })
  const contentType = getHeader(response.headers, 'content-type') || response.data.type || 'application/octet-stream'
  const filename = parseDownloadFilename(
    getHeader(response.headers, 'content-disposition'),
    fallbackName
  )

  return {
    blob: response.data,
    filename,
    contentType,
  }
}

export async function downloadChatAttachment(id: number): Promise<WebChatDownload> {
  return downloadChatBlob(`/chat/attachments/${id}/download`, `chat-attachment-${id}`)
}

export async function downloadChatArtifact(id: number): Promise<WebChatDownload> {
  return downloadChatBlob(`/chat/artifacts/${id}/download`, `chat-artifact-${id}`)
}

export const chatAPI = {
  listModels: listChatModels,
  listConversations: listChatConversations,
  getConversation: getChatConversation,
  createConversation: createChatConversation,
  updateConversation: updateChatConversation,
  deleteConversation: deleteChatConversation,
  uploadAttachment: uploadChatAttachment,
  cancelMessage: cancelChatMessage,
  sendMessageStream: sendChatMessageStream,
  attachmentDownloadUrl: chatAttachmentDownloadUrl,
  artifactDownloadUrl: chatArtifactDownloadUrl,
  downloadAttachment: downloadChatAttachment,
  downloadArtifact: downloadChatArtifact,
}
