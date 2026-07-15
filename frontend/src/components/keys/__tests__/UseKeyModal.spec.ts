import { describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn().mockResolvedValue(true)
  })
}))

import UseKeyModal from '../UseKeyModal.vue'
import {
  buildClientConfigFiles,
  type SupportedGuideClient,
  type SupportedGuideOS,
  type WindowsGuideShell
} from '../clientConfigFiles'
import zhDashboard from '@/i18n/locales/zh/dashboard'
import enDashboard from '@/i18n/locales/en/dashboard'

const modalStubs = {
  BaseDialog: {
    template: '<div><slot /><slot name="footer" /></div>'
  },
  Icon: {
    template: '<span />'
  }
}

function generatedFiles(wrapper: VueWrapper): Array<{ path: string; content: string }> {
  return wrapper.findAll('.linx-code-panel').map((panel) => ({
    path: panel.find('span.font-mono').text(),
    content: panel.find('pre code').text()
  }))
}

function generatedFileContent(wrapper: VueWrapper, pathSuffix: string): string {
  const panel = wrapper.findAll('.linx-code-panel').find((candidate) =>
    candidate.find('span.font-mono').text().endsWith(pathSuffix)
  )

  expect(panel, `generated file ending in ${pathSuffix}`).toBeDefined()
  return panel!.find('pre code').text()
}

async function mountCodexExample(platform: 'openai' | 'unified', baseUrl: string) {
  const wrapper = mount(UseKeyModal, {
    props: {
      show: true,
      apiKey: 'sk-test',
      baseUrl,
      platform
    },
    global: {
      stubs: {
        BaseDialog: {
          template: '<div><slot /><slot name="footer" /></div>'
        },
        Icon: {
          template: '<span />'
        }
      }
    }
  })

  const codexTab = wrapper.findAll('nav[aria-label="Client"] button').find((button) =>
    button.text().includes('keys.useKeyModal.cliTabs.codexCli')
  )

  expect(codexTab).toBeDefined()
  await codexTab!.trigger('click')
  await nextTick()

  return wrapper
}

function expectCodexFileContract(wrapper: VueWrapper, expectedBaseUrl: string) {
  const configToml = generatedFileContent(wrapper, 'config.toml')
  const authJson = generatedFileContent(wrapper, 'auth.json')

  expect(configToml).toContain('model_provider = "OpenAI"')
  expect(configToml).toContain('model = "gpt-5.5"')
  expect(configToml).toContain('review_model = "gpt-5.5"')
  expect(configToml).toContain(`[model_providers.OpenAI]\nname = "OpenAI"\nbase_url = "${expectedBaseUrl}"`)
  expect(configToml).toContain('wire_api = "responses"')
  expect(configToml).toContain('requires_openai_auth = true')
  expect(configToml).toContain('[features]\ngoals = true')
  expect(configToml).not.toContain('sk-test')
  expect(configToml).not.toContain('env_key')
  expect(configToml).not.toMatch(/^\s*(?:api_)?key\s*=/m)
  expect(JSON.parse(authJson)).toEqual({ OPENAI_API_KEY: 'sk-test' })
}

describe('UseKeyModal', () => {
  it.each([
    {
      name: 'Claude Unix',
      client: 'claude_code' as SupportedGuideClient,
      os: 'macos' as SupportedGuideOS,
      clientLabel: null,
      shellLabel: null,
      windowsShell: undefined
    },
    {
      name: 'Claude PowerShell',
      client: 'claude_code' as SupportedGuideClient,
      os: 'windows' as SupportedGuideOS,
      clientLabel: null,
      shellLabel: 'PowerShell',
      windowsShell: 'powershell' as WindowsGuideShell
    },
    {
      name: 'Claude CMD',
      client: 'claude_code' as SupportedGuideClient,
      os: 'windows' as SupportedGuideOS,
      clientLabel: null,
      shellLabel: 'Windows CMD',
      windowsShell: 'cmd' as WindowsGuideShell
    },
    {
      name: 'Codex Unix',
      client: 'codex' as SupportedGuideClient,
      os: 'macos' as SupportedGuideOS,
      clientLabel: 'keys.useKeyModal.cliTabs.codexCli',
      shellLabel: null,
      windowsShell: undefined
    },
    {
      name: 'Codex Windows',
      client: 'codex' as SupportedGuideClient,
      os: 'windows' as SupportedGuideOS,
      clientLabel: 'keys.useKeyModal.cliTabs.codexCli',
      shellLabel: 'Windows',
      windowsShell: undefined
    }
  ])('matches shared configuration paths and contents for $name', async ({
    client,
    os,
    clientLabel,
    shellLabel,
    windowsShell
  }) => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://gateway.example.com/v1/',
        platform: 'unified'
      },
      global: { stubs: modalStubs }
    })

    if (clientLabel) {
      const clientTab = wrapper.findAll('nav[aria-label="Client"] button').find((button) =>
        button.text().includes(clientLabel)
      )
      expect(clientTab).toBeDefined()
      await clientTab!.trigger('click')
      await nextTick()
    }

    if (shellLabel) {
      const shellTab = wrapper.findAll('nav[aria-label="Tabs"] button').find((button) =>
        button.text().trim() === shellLabel
      )
      expect(shellTab).toBeDefined()
      await shellTab!.trigger('click')
      await nextTick()
    }

    const expected = buildClientConfigFiles({
      client,
      os,
      platform: 'unified',
      apiKey: 'sk-test',
      baseUrl: 'https://gateway.example.com/v1/',
      windowsShell
    }).map(({ path, content }) => ({ path, content }))

    expect(generatedFiles(wrapper)).toEqual(expected)
  })

  it('renders Grok Build and OpenCode setup for Grok groups', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-grok-test',
        baseUrl: 'https://example.com/v1',
        platform: 'grok'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const grokTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.grokCli')
    )
    expect(grokTab).toBeDefined()

    const grokConfig = wrapper.findAll('pre code')
      .map((code) => code.text())
      .find((content) => content.includes('[model."sub2api-grok"]'))
    expect(grokConfig).toBeDefined()
    expect(grokConfig).toContain('model = "grok-4.5"')
    expect(grokConfig).toContain('base_url = "https://example.com/v1"')
    expect(grokConfig).toContain('api_key = "sk-grok-test"')
    expect(grokConfig).toContain('api_backend = "responses"')

    const windowsTab = wrapper.findAll('button').find(
      (button) => button.text().trim() === 'Windows'
    )
    expect(windowsTab).toBeDefined()
    await windowsTab!.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('%userprofile%\\.grok/config.toml')

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )
    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const parsed = JSON.parse(wrapper.find('pre code').text())
    expect(parsed.provider.grok.npm).toBe('@ai-sdk/openai')
    expect(parsed.provider.grok.options).toEqual({
      baseURL: 'https://example.com/v1',
      apiKey: 'sk-grok-test'
    })
    expect(parsed.provider.grok.models['grok-4.5']).toBeDefined()
    expect(parsed.provider.grok.models['grok-build-0.1']).toBeDefined()
    expect(parsed.provider.grok.models['grok-composer-2.5-fast']).toBeDefined()
    expect(parsed.provider.grok.models['gpt-5.6']).toBeUndefined()
  })

  it('orders OpenAI usage tabs with Claude Code, Codex, OpenCode, then SDK examples', () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai',
        allowMessagesDispatch: true
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const tabLabels = wrapper.findAll('nav[aria-label="Client"] button').map((button) => button.text())

    expect(tabLabels).toEqual([
      'keys.useKeyModal.cliTabs.claudeCode',
      'keys.useKeyModal.cliTabs.codexCli',
      'keys.useKeyModal.cliTabs.workBuddy',
      'keys.useKeyModal.cliTabs.opencode',
      'keys.useKeyModal.cliTabs.openaiPythonSdk',
      'keys.useKeyModal.cliTabs.openaiImagen2PythonSdk'
    ])
    expect(tabLabels).not.toContain('keys.useKeyModal.cliTabs.codexCliWs')
  })

  it('keeps both unified SDK tabs, appends GPT Image 2, and wraps client tabs', () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'unified'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const clientNav = wrapper.find('nav[aria-label="Client"]')
    const tabLabels = clientNav.findAll('button').map((button) => button.text())

    expect(tabLabels).toEqual([
      'keys.useKeyModal.cliTabs.claudeCode',
      'keys.useKeyModal.cliTabs.codexCli',
      'keys.useKeyModal.cliTabs.workBuddy',
      'keys.useKeyModal.cliTabs.opencode',
      'keys.keyTypes.anthropic keys.useKeyModal.cliTabs.anthropicPythonSdk',
      'keys.keyTypes.openai keys.useKeyModal.cliTabs.openaiPythonSdk',
      'keys.useKeyModal.cliTabs.openaiImagen2PythonSdk'
    ])
    expect(clientNav.classes()).toEqual(
      expect.arrayContaining(['flex', 'flex-wrap', 'gap-x-6', 'gap-y-1'])
    )
    expect(clientNav.classes()).not.toContain('space-x-6')
    expect(clientNav.classes()).not.toContain('overflow-x-auto')
  })

  it('renders WorkBuddy models.json with gateway-supported model ids', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'unified'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const workBuddyTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.workBuddy')
    )

    expect(workBuddyTab).toBeDefined()
    await workBuddyTab!.trigger('click')
    await nextTick()

    const filePath = wrapper.find('.linx-code-panel span')
    expect(filePath.text()).toBe('~/.workbuddy/models.json')

    const codeBlock = wrapper.find('pre code')
    expect(codeBlock.exists()).toBe(true)
    expect(wrapper.text()).toContain('keys.useKeyModal.workBuddy.description')
    expect(wrapper.text()).toContain('keys.useKeyModal.workBuddy.note')

    const parsed = JSON.parse(codeBlock.text())
    expect(parsed.availableModels).toEqual(['gpt-5.5', 'claude-sonnet-5', 'claude-opus-4-8'])
    expect(parsed.models.map((model: any) => model.id)).toEqual(parsed.availableModels)
    expect(parsed.models.map((model: any) => model.name)).toEqual(parsed.availableModels)
    expect(parsed.models.every((model: any) => model.url === 'https://example.com/v1/chat/completions')).toBe(true)
    expect(parsed.models.every((model: any) => model.apiKey === 'sk-test')).toBe(true)
    expect(parsed.models.every((model: any) => model.vendor === 'Custom')).toBe(true)
    expect(codeBlock.text()).not.toContain('Claude Sonnet 5')
    expect(codeBlock.text()).not.toContain('Claude Opus 4.8')
  })

  it('renders Anthropic Python SDK client config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'anthropic'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const sdkTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.anthropicPythonSdk')
    )

    expect(sdkTab).toBeDefined()
    await sdkTab!.trigger('click')
    await nextTick()

    const codeBlock = wrapper.find('pre code')
    expect(codeBlock.exists()).toBe(true)
    expect(codeBlock.text()).toContain('from anthropic import Anthropic')
    expect(codeBlock.text()).toContain('client = Anthropic(')
    expect(codeBlock.text()).toContain('api_key="sk-test"')
    // IMPORTANT: base_url must NOT include /v1. The Anthropic Python SDK posts to
    // its hardcoded /v1/messages endpoint, so a /v1-suffixed base_url would
    // produce /v1/v1/messages and 404. Do not "fix" this back to /v1.
    expect(codeBlock.text()).toContain('base_url="https://example.com"')
    expect(codeBlock.text()).not.toContain('base_url="https://example.com/v1"')
  })

  it('strips trailing /v1 from Anthropic Python SDK base_url when admin set it on baseUrl', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'anthropic'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const sdkTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.anthropicPythonSdk')
    )

    expect(sdkTab).toBeDefined()
    await sdkTab!.trigger('click')
    await nextTick()

    const codeBlock = wrapper.find('pre code')
    expect(codeBlock.exists()).toBe(true)
    // Even when admin's api_base_url ends with /v1, the generated example must
    // pass a bare base_url to the Anthropic Python SDK.
    expect(codeBlock.text()).toContain('base_url="https://example.com"')
    expect(codeBlock.text()).not.toContain('base_url="https://example.com/v1"')
  })

  it('renders Python SDK guidance for SDK tabs', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const sdkTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.openaiPythonSdk')
    )

    expect(sdkTab).toBeDefined()
    await sdkTab!.trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('keys.useKeyModal.pythonSdk.description')
    expect(wrapper.text()).toContain('keys.useKeyModal.pythonSdk.note')
    expect(wrapper.text()).not.toContain('keys.useKeyModal.openai.description')
    expect(wrapper.text()).not.toContain('keys.useKeyModal.openai.note')
  })

  it('renders OpenAI Python SDK responses config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const sdkTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.openaiPythonSdk')
    )

    expect(sdkTab).toBeDefined()
    await sdkTab!.trigger('click')
    await nextTick()

    const codeBlock = wrapper.find('pre code')
    expect(codeBlock.exists()).toBe(true)
    expect(codeBlock.text()).toContain('from openai import OpenAI')
    expect(codeBlock.text()).toContain('client = OpenAI(')
    expect(codeBlock.text()).toContain('api_key="sk-test"')
    expect(codeBlock.text()).toContain('base_url="https://example.com/v1"')
    expect(codeBlock.text()).toContain('stream = client.responses.create(')
    expect(codeBlock.text()).toContain('model="gpt-5.5"')
    expect(codeBlock.text()).toContain('stream=True')
    expect(codeBlock.text()).toContain('print(event.delta, end="", flush=True)')
  })

  it.each(['openai', 'unified'] as const)(
    'renders a streaming GPT Image 2 Python SDK example for %s keys',
    async (platform) => {
      const wrapper = mount(UseKeyModal, {
        props: {
          show: true,
          apiKey: 'sk-test',
          baseUrl: 'https://example.com',
          platform
        },
        global: {
          stubs: {
            BaseDialog: {
              template: '<div><slot /><slot name="footer" /></div>'
            },
            Icon: {
              template: '<span />'
            }
          }
        }
      })

      const imagenTab = wrapper.findAll('button').find((button) =>
        button.text().includes('keys.useKeyModal.cliTabs.openaiImagen2PythonSdk')
      )

      expect(imagenTab).toBeDefined()
      await imagenTab!.trigger('click')
      await nextTick()

      const codeBlock = wrapper.find('pre code')
      expect(codeBlock.exists()).toBe(true)
      expect(codeBlock.text()).toContain('from base64 import b64decode')
      expect(codeBlock.text()).toContain('from pathlib import Path')
      expect(codeBlock.text()).toContain('from openai import OpenAI')
      expect(codeBlock.text()).toContain('api_key="sk-test"')
      expect(codeBlock.text()).toContain('base_url="https://example.com/v1"')
      expect(codeBlock.text()).toContain('stream = client.images.generate(')
      expect(codeBlock.text()).toContain('model="gpt-image-2"')
      expect(codeBlock.text()).not.toContain('model="imagen-2"')
      expect(codeBlock.text()).toContain('prompt="A fox mascot using an AI gateway"')
      expect(codeBlock.text()).toContain('stream=True')
      expect(codeBlock.text()).toContain('partial_images=2')
      expect(codeBlock.text()).toContain('event.type == "image_generation.partial_image"')
      expect(codeBlock.text()).toContain('event.type == "image_generation.completed"')
      expect(codeBlock.text()).toContain('image_b64 = getattr(event, "b64_json", None)')
      expect(codeBlock.text()).toContain('if not image_b64:\n        continue')
      expect(codeBlock.text()).toContain('Path(f"partial_{event.partial_image_index}.png")')
      expect(codeBlock.text()).toContain('Path("image.png")')
      expect(codeBlock.text()).toContain('else:\n        continue')
      expect(codeBlock.text()).toContain('output_path.write_bytes(b64decode(image_b64))')
      expect(codeBlock.text()).toContain('print(f"Wrote {output_path}")')
    }
  )

  it('labels the image SDK tab as GPT Image 2 in Chinese and English', () => {
    expect(zhDashboard.keys.useKeyModal.cliTabs.openaiImagen2PythonSdk).toBe(
      'GPT Image 2 Python SDK'
    )
    expect(enDashboard.keys.useKeyModal.cliTabs.openaiImagen2PythonSdk).toBe(
      'GPT Image 2 Python SDK'
    )
  })

  it.each([
    ['openai' as const, 'https://example.com', 'https://example.com/v1'],
    ['openai' as const, 'https://example.com/v1/', 'https://example.com/v1'],
    ['unified' as const, 'https://example.com/v1/', 'https://example.com/v1']
  ])('renders a complete %s Codex config/auth contract for %s', async (platform, baseUrl, expectedBaseUrl) => {
    const wrapper = await mountCodexExample(platform, baseUrl)

    expectCodexFileContract(wrapper, expectedBaseUrl)
  })

  it('shows Codex-specific guidance when a unified key selects Codex', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com',
        platform: 'unified'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const codexTab = wrapper.findAll('nav[aria-label="Client"] button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.codexCli')
    )

    expect(codexTab).toBeDefined()
    await codexTab!.trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('keys.useKeyModal.openai.description')
    expect(wrapper.find('div.bg-blue-50 > p').text()).toBe('keys.useKeyModal.openai.note')
    expect(wrapper.text()).not.toContain('keys.useKeyModal.unified.note')

    const windowsTab = wrapper.findAll('nav[aria-label="Tabs"] button').find((button) =>
      button.text().includes('Windows')
    )
    expect(windowsTab).toBeDefined()
    await windowsTab!.trigger('click')
    await nextTick()

    expect(wrapper.find('div.bg-blue-50 > p').text()).toBe('keys.useKeyModal.openai.noteWindows')
  })

  it('documents safe Codex auth.json handling in Chinese and English', () => {
    const zhOpenAI = zhDashboard.keys.useKeyModal.openai
    const enOpenAI = enDashboard.keys.useKeyModal.openai

    expect(zhOpenAI.description).toContain('config.toml')
    expect(zhOpenAI.description).toContain('auth.json')
    expect(zhOpenAI.note).toContain('OPENAI_API_KEY')
    expect(zhOpenAI.note).toContain('env_key')
    expect(zhOpenAI.note).toContain('重启 Codex')
    expect(zhOpenAI.noteWindows).toContain('OPENAI_API_KEY')
    expect(zhOpenAI.noteWindows).toContain('env_key')
    expect(zhOpenAI.noteWindows).toContain('重启 Codex')
    expect(enOpenAI.description).toContain('config.toml')
    expect(enOpenAI.description).toContain('auth.json')
    expect(enOpenAI.note).toContain('OPENAI_API_KEY')
    expect(enOpenAI.note).toContain('env_key')
    expect(enOpenAI.note).toContain('restart Codex')
    expect(enOpenAI.noteWindows).toContain('OPENAI_API_KEY')
    expect(enOpenAI.noteWindows).toContain('env_key')
    expect(enOpenAI.noteWindows).toContain('restart Codex')
  })

  it('renders GPT-5.4 mini entry in OpenCode config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )

    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const codeBlock = wrapper.find('pre code')
    expect(codeBlock.exists()).toBe(true)
    expect(codeBlock.text()).toContain('"name": "GPT-5.4 Mini"')
    expect(codeBlock.text()).not.toContain('"name": "GPT-5.4 Nano"')

    const parsed = JSON.parse(codeBlock.text())
    expect(parsed.$schema).toBe('https://opencode.ai/config.json')
    expect(parsed.model).toBe('openai/gpt-5.5')
    expect(parsed.small_model).toBe('openai/gpt-5.3-codex-spark')
    expect(parsed.provider.openai.options).toEqual({
      baseURL: 'https://example.com/v1',
      apiKey: 'sk-test'
    })
    expect(Object.keys(parsed.provider.openai.models)).toEqual([
      'gpt-5.6',
      'gpt-5.6-sol',
      'gpt-5.6-terra',
      'gpt-5.6-luna',
      'gpt-5.5',
      'gpt-5.4',
      'gpt-5.4-mini',
      'gpt-5.3-codex-spark',
      'gpt-5.2',
      'codex-mini-latest'
    ])
    expect(parsed.provider.openai.models).not.toHaveProperty('codex-auto-review')
    expect(parsed.provider.openai.models).not.toHaveProperty('gpt-image-2')
    expect(parsed.provider.openai.models).not.toHaveProperty('gpt-4o-audio-preview')
    expect(parsed.provider.openai.models).not.toHaveProperty('gpt-4o-realtime-preview')
    expect(wrapper.text()).toContain('keys.useKeyModal.opencode.description')
    expect(wrapper.text()).not.toContain('keys.useKeyModal.openai.description')
  })

  it('renders current Claude models in Anthropic OpenCode config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'anthropic'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )

    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const parsed = JSON.parse(wrapper.find('pre code').text())
    expect(parsed.model).toBe('anthropic/claude-fable-5')
    expect(parsed.small_model).toBe('anthropic/claude-haiku-4-5-20251001')
    expect(parsed.provider.anthropic.options).toEqual({
      baseURL: 'https://example.com/v1',
      apiKey: 'sk-test'
    })
    expect(Object.keys(parsed.provider.anthropic.models)).toEqual([
      'claude-fable-5',
      'claude-mythos-5',
      'claude-opus-4-8',
      'claude-opus-4-7',
      'claude-opus-4-6',
      'claude-opus-4-5-20251101',
      'claude-sonnet-4-6',
      'claude-sonnet-4-5-20250929',
      'claude-haiku-4-5-20251001',
      'claude-sonnet-4-20250514',
      'claude-opus-4-20250514',
      'claude-opus-4-1-20250805',
      'claude-3-7-sonnet-20250219',
      'claude-3-5-sonnet-20241022',
      'claude-3-5-sonnet-20240620',
      'claude-3-5-haiku-20241022'
    ])
  })

  it('renders GPT-5.6 alias and max variants in OpenCode config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )
    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const parsed = JSON.parse(wrapper.find('pre code').text())
    const models = parsed.provider.openai.models
    for (const model of ['gpt-5.6', 'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna']) {
      expect(models[model]).toBeDefined()
      expect(models[model].variants).toHaveProperty('max')
      expect(models[model].variants).toHaveProperty('xhigh')
    }
    expect(models['gpt-5.6'].name).toBe('GPT-5.6 (Sol)')
  })

  it('renders Claude Fable 5 OpenCode config with adaptive thinking', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'antigravity'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )

    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const claudeConfig = wrapper.findAll('pre code')
      .map((code) => code.text())
      .find((content) => content.includes('"antigravity-claude"'))

    expect(claudeConfig).toBeDefined()
    const parsed = JSON.parse(claudeConfig!)
    const models = parsed.provider['antigravity-claude'].models
    const fable = models['claude-fable-5']
    const mythos = models['claude-mythos-5']

    expect(fable.name).toBe('Claude Fable 5')
    expect(fable.limit).toEqual({ context: 1048576, output: 128000 })
    expect(fable.options.thinking).toEqual({ type: 'adaptive' })
    expect(fable.options.thinking).not.toHaveProperty('budgetTokens')
    expect(mythos.name).toBe('Claude Mythos 5')
    expect(mythos.limit).toEqual({ context: 1048576, output: 128000 })
    expect(mythos.options.thinking).toEqual({ type: 'adaptive' })
    expect(mythos.options.thinking).not.toHaveProperty('budgetTokens')
  })
})
