import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import EmailOAuthButtons from '@/components/auth/EmailOAuthButtons.vue'

const { showErrorMock } = vi.hoisted(() => ({
  showErrorMock: vi.fn(),
}))

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, unknown>,
}))

const locationState = vi.hoisted(() => ({
  current: {
    href: 'http://localhost/register?aff=AFF123',
    hostname: 'localhost',
  } as { href: string; hostname: string },
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState,
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, string>) => {
      if (key === 'auth.emailOAuth.signIn') {
        return `使用 ${params?.providerName ?? ''} 登录`
      }
      return key
    },
  }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError: (...args: unknown[]) => showErrorMock(...args),
  }),
}))

describe('EmailOAuthButtons', () => {
  beforeEach(() => {
    showErrorMock.mockReset()
    routeState.query = { redirect: '/billing?plan=pro', aff: 'AFF123' }
    locationState.current = {
      href: 'http://localhost/register?aff=AFF123',
      hostname: 'localhost',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState.current,
    })
    window.localStorage.clear()
    window.sessionStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllEnvs()
  })

  it('passes the affiliate code to the email oauth start URL', async () => {
    const wrapper = mount(EmailOAuthButtons, {
      props: {
        githubEnabled: true,
        googleEnabled: false,
      },
      global: {
        stubs: {
          GitHubMark: true,
          GoogleMark: true,
        },
      },
    })

    await wrapper.get('button').trigger('click')

    expect(locationState.current.href).toBe(
      '/api/v1/auth/oauth/github/start?redirect=%2Fbilling%3Fplan%3Dpro&aff_code=AFF123'
    )
    expect(window.sessionStorage.getItem('oauth_aff_code')).toBe('AFF123')
    expect(window.sessionStorage.getItem('email_oauth_pending_provider')).toBe('github')
  })

  it.each(['github', 'google'] as const)(
    'routes %s oauth through the canonical host on yundu.linx2.ai',
    async (provider) => {
      routeState.query = { redirect: '/dashboard', aff: 'OTHER' }
      locationState.current = {
        href: 'https://yundu.linx2.ai/register?aff=OTHER',
        hostname: 'yundu.linx2.ai',
      }
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: locationState.current,
      })
      const wrapper = mount(EmailOAuthButtons, {
        props: {
          affCode: 'OTHER',
          githubEnabled: provider === 'github',
          googleEnabled: provider === 'google',
        },
        global: {
          stubs: {
            GitHubMark: true,
            GoogleMark: true,
          },
        },
      })

      await wrapper.get('button').trigger('click')

      expect(locationState.current.href).toBe(
        `https://www.linx2.ai/api/v1/auth/oauth/${provider}/start?redirect=%2Fdashboard&aff_code=YUNDU`
      )
      expect(window.sessionStorage.getItem('oauth_aff_code')).toBe('YUNDU')
      expect(window.sessionStorage.getItem('email_oauth_pending_provider')).toBe(provider)
    }
  )

  it('shows a login error without storing pending oauth state when the API base is invalid', async () => {
    vi.stubEnv('VITE_API_BASE_URL', 'http://[')
    routeState.query = { redirect: '/dashboard', aff: 'OTHER' }
    locationState.current = {
      href: 'https://yundu.linx2.ai/register?aff=OTHER',
      hostname: 'yundu.linx2.ai',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState.current,
    })
    const wrapper = mount(EmailOAuthButtons, {
      props: {
        affCode: 'OTHER',
        githubEnabled: true,
        googleEnabled: false,
      },
      global: {
        stubs: {
          GitHubMark: true,
          GoogleMark: true,
        },
      },
    })
    const preventUnhandledError = vi.fn((event: ErrorEvent) => event.preventDefault())
    window.addEventListener('error', preventUnhandledError)

    await expect(wrapper.get('button').trigger('click')).resolves.toBeUndefined()
    window.removeEventListener('error', preventUnhandledError)

    expect(preventUnhandledError).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledWith('auth.loginFailed')
    expect(locationState.current.href).toBe('https://yundu.linx2.ai/register?aff=OTHER')
    expect(window.sessionStorage.getItem('oauth_aff_code')).toBeNull()
    expect(window.sessionStorage.getItem('email_oauth_pending_provider')).toBeNull()
  })

  it.each([
    ['a query', 'https://api.example.com/api/v1?tenant=x'],
    ['a fragment', 'https://api.example.com/api/v1#fragment'],
    ['a non-http protocol', 'ftp://api.example.com/api/v1'],
    ['an empty query delimiter', 'https://api.example.com/api/v1?'],
    ['an empty fragment delimiter', 'https://api.example.com/api/v1#'],
    ['empty query and fragment delimiters', 'https://api.example.com/api/v1?#'],
  ])(
    'rejects an API base with %s without storing pending oauth state on yundu',
    async (_label, apiBase) => {
      vi.stubEnv('VITE_API_BASE_URL', apiBase)
      routeState.query = { redirect: '/dashboard', aff: 'OTHER' }
      locationState.current = {
        href: 'https://yundu.linx2.ai/register?aff=OTHER',
        hostname: 'yundu.linx2.ai',
      }
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: locationState.current,
      })
      const wrapper = mount(EmailOAuthButtons, {
        props: {
          affCode: 'OTHER',
          githubEnabled: true,
          googleEnabled: false,
        },
        global: {
          stubs: {
            GitHubMark: true,
            GoogleMark: true,
          },
        },
      })

      await wrapper.get('button').trigger('click')

      expect(showErrorMock).toHaveBeenCalledWith('auth.loginFailed')
      expect(locationState.current.href).toBe('https://yundu.linx2.ai/register?aff=OTHER')
      expect(window.sessionStorage.getItem('oauth_aff_code')).toBeNull()
      expect(window.sessionStorage.getItem('email_oauth_pending_provider')).toBeNull()
    }
  )

  it('preserves an absolute API base and removes repeated trailing slashes on yundu', async () => {
    vi.stubEnv('VITE_API_BASE_URL', 'https://api.example.com/api/v1///')
    routeState.query = { redirect: '/dashboard', aff: 'OTHER' }
    locationState.current = {
      href: 'https://yundu.linx2.ai/register?aff=OTHER',
      hostname: 'yundu.linx2.ai',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState.current,
    })
    const wrapper = mount(EmailOAuthButtons, {
      props: {
        affCode: 'OTHER',
        githubEnabled: true,
        googleEnabled: false,
      },
      global: {
        stubs: {
          GitHubMark: true,
          GoogleMark: true,
        },
      },
    })

    await wrapper.get('button').trigger('click')

    expect(locationState.current.href).toBe(
      'https://api.example.com/api/v1/auth/oauth/github/start?redirect=%2Fdashboard&aff_code=YUNDU'
    )
  })

  it('preserves encoded query and fragment characters in an absolute API base path', async () => {
    vi.stubEnv('VITE_API_BASE_URL', 'https://api.example.com/api%3Fv1/%23segment')
    routeState.query = { redirect: '/dashboard', aff: 'OTHER' }
    locationState.current = {
      href: 'https://yundu.linx2.ai/register?aff=OTHER',
      hostname: 'yundu.linx2.ai',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState.current,
    })
    const wrapper = mount(EmailOAuthButtons, {
      props: {
        affCode: 'OTHER',
        githubEnabled: true,
        googleEnabled: false,
      },
      global: {
        stubs: {
          GitHubMark: true,
          GoogleMark: true,
        },
      },
    })

    await wrapper.get('button').trigger('click')

    expect(locationState.current.href).toBe(
      'https://api.example.com/api%3Fv1/%23segment/auth/oauth/github/start?redirect=%2Fdashboard&aff_code=YUNDU'
    )
    expect(window.sessionStorage.getItem('oauth_aff_code')).toBe('YUNDU')
    expect(window.sessionStorage.getItem('email_oauth_pending_provider')).toBe('github')
  })

  it('keeps the oauth start URL relative on www.linx2.ai', async () => {
    routeState.query = { redirect: '/dashboard', aff: 'AFF123' }
    locationState.current = {
      href: 'https://www.linx2.ai/register?aff=AFF123',
      hostname: 'www.linx2.ai',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState.current,
    })
    const wrapper = mount(EmailOAuthButtons, {
      props: {
        affCode: 'AFF123',
        githubEnabled: true,
        googleEnabled: false,
      },
      global: {
        stubs: {
          GitHubMark: true,
          GoogleMark: true,
        },
      },
    })

    await wrapper.get('button').trigger('click')

    expect(locationState.current.href).toBe(
      '/api/v1/auth/oauth/github/start?redirect=%2Fdashboard&aff_code=AFF123'
    )
    expect(window.sessionStorage.getItem('oauth_aff_code')).toBe('AFF123')
    expect(window.sessionStorage.getItem('email_oauth_pending_provider')).toBe('github')
  })

  it('uses a full-width descriptive button when only GitHub is enabled', () => {
    const wrapper = mount(EmailOAuthButtons, {
      props: {
        githubEnabled: true,
        googleEnabled: false,
      },
      global: {
        stubs: {
          GitHubMark: true,
          GoogleMark: true,
        },
      },
    })

    expect(wrapper.find('.grid').classes()).not.toContain('sm:grid-cols-2')
    expect(wrapper.get('button').text()).toContain('使用 GitHub 登录')
  })

  it('uses compact labels and two columns when GitHub and Google are both enabled', () => {
    const wrapper = mount(EmailOAuthButtons, {
      props: {
        githubEnabled: true,
        googleEnabled: true,
      },
      global: {
        stubs: {
          GitHubMark: true,
          GoogleMark: true,
        },
      },
    })

    expect(wrapper.find('.grid').classes()).toContain('sm:grid-cols-2')
    const buttons = wrapper.findAll('button')
    expect(buttons).toHaveLength(2)
    expect(buttons[0].text()).toContain('GitHub')
    expect(buttons[0].text()).not.toContain('使用 GitHub 登录')
    expect(buttons[1].text()).toContain('Google')
    expect(buttons[1].text()).not.toContain('使用 Google 登录')
  })
})
