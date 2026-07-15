const IMAGE_ACCEPT = ['image/png', 'image/jpeg', 'image/webp', 'image/gif']

const LEGACY_FILE_EXTENSIONS = ['.pdf', '.docx', '.txt', '.md', '.markdown', '.json', '.csv']

const OPENAI_FILE_EXTENSIONS = [
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

const OPENAI_SPECIAL_FILE_ACCEPT = ['text/x-dockerfile']

export function webChatAttachmentAccept(provider?: string): string {
  const isOpenAI = provider?.trim().toLowerCase() === 'openai'
  const extensions = isOpenAI ? OPENAI_FILE_EXTENSIONS : LEGACY_FILE_EXTENSIONS
  const special = isOpenAI ? OPENAI_SPECIAL_FILE_ACCEPT : []
  return [...IMAGE_ACCEPT, ...extensions, ...special].join(',')
}
