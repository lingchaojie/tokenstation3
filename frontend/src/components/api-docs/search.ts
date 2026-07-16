import { API_DOCS_PAGES, API_ENDPOINTS } from './catalog'

export interface ApiDocsSearchEntry {
  id: string
  path: string
  title: string
  section: string
  text: string
}

export function buildApiDocsSearchEntries(
  t: (key: string) => string
): ApiDocsSearchEntry[] {
  return API_DOCS_PAGES.map((page) => {
    const endpoint = API_ENDPOINTS.find(({ pageId }) => pageId === page.id)
    const title = t(page.titleKey)
    const section = t(`apiDocs.searchCategories.${page.kind}`)
    const text = [
      title,
      t(page.summaryKey),
      section,
      endpoint?.path ?? '',
      ...(endpoint?.errorCodes ?? []),
      ...page.keywords
    ]
      .join(' ')
      .toLowerCase()

    return {
      id: page.id,
      path: page.path,
      title,
      section,
      text
    }
  })
}
