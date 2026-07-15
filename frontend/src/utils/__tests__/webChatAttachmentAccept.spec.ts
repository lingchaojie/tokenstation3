import { describe, expect, it } from 'vitest'
import { webChatAttachmentAccept } from '@/utils/webChatAttachmentAccept'

const imageAccept = ['image/png', 'image/jpeg', 'image/webp', 'image/gif']

const openAIFileExtensions = [
  '.pdf', '.doc', '.docx', '.dot', '.odt', '.rtf', '.pages',
  '.pot', '.ppa', '.pps', '.ppt', '.pptx', '.pwz', '.wiz', '.key',
  '.xla', '.xlb', '.xlc', '.xlm', '.xls', '.xlsx', '.xlt', '.xlw', '.csv', '.tsv', '.iif',
  '.asm', '.bat', '.c', '.cc', '.conf', '.cpp', '.css', '.cxx', '.def', '.dic', '.eml',
  '.h', '.hh', '.htm', '.html', '.ics', '.ifb', '.in', '.js', '.json', '.ksh', '.list',
  '.log', '.markdown', '.md', '.mht', '.mhtml', '.mime', '.mjs', '.nws', '.pl', '.py',
  '.rst', '.s', '.sql', '.srt', '.text', '.txt', '.vcf', '.vtt', '.xml', '.ts', '.tsx',
  '.jsx', '.java', '.go', '.rs', '.scala', '.ps1', '.diff', '.patch', '.php', '.rb', '.sh',
  '.bash', '.zsh', '.tex', '.cs', '.kt', '.kts', '.swift', '.lua', '.r', '.jl', '.m', '.mm',
  '.erl', '.ex', '.exs', '.hs', '.clj', '.cljs', '.cljc', '.groovy', '.dart', '.awk',
  '.hbs', '.mustache', '.ejs', '.jinja', '.jinja2', '.liquid', '.erb', '.twig', '.pug',
  '.jade', '.tmpl', '.cmake', '.gradle', '.ini', '.properties', '.proto', '.scss', '.sass',
  '.less', '.hcl', '.tf', '.toml', '.graphql', '.ndjson', '.json5', '.yaml', '.yml', '.astro',
]

describe('webChatAttachmentAccept', () => {
  it('matches the OpenAI file registry and image/Dockerfile hints', () => {
    expect(webChatAttachmentAccept(' OpenAI ').split(',')).toEqual([
      ...imageAccept,
      ...openAIFileExtensions,
      'text/x-dockerfile',
    ])
  })

  it('keeps non-OpenAI hints on the legacy range', () => {
    expect(webChatAttachmentAccept('anthropic').split(',')).toEqual([
      ...imageAccept,
      '.pdf',
      '.docx',
      '.txt',
      '.md',
      '.markdown',
      '.json',
      '.csv',
    ])
  })
})
