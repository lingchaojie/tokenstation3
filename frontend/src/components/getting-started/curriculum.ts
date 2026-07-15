import {
  BEGINNER_GUIDE_STEP_ORDER,
  type BeginnerGuideClient,
  type BeginnerGuideOS
} from '@/api/beginnerGuide'

export interface GuideVariant {
  client: BeginnerGuideClient
  os: BeginnerGuideOS
  shell: 'terminal' | 'powershell'
  installCommand: string
  desktopDownloadUrl?: string
  verifyCommand: string
  launchCommand: string
  diagnosticCommands: string[]
  officialSourceUrl: string
  verifiedAt: '2026-07-15'
}

export const GUIDE_STEP_IDS = BEGINNER_GUIDE_STEP_ORDER

export const GUIDE_VARIANTS: GuideVariant[] = [
  {
    client: 'claude_code',
    os: 'macos',
    shell: 'terminal',
    installCommand: 'curl -fsSL https://claude.ai/install.sh | bash',
    verifyCommand: 'claude --version',
    launchCommand: 'claude',
    diagnosticCommands: ['claude doctor'],
    officialSourceUrl: 'https://code.claude.com/docs/en/installation',
    verifiedAt: '2026-07-15'
  },
  {
    client: 'claude_code',
    os: 'windows',
    shell: 'powershell',
    installCommand: 'irm https://claude.ai/install.ps1 | iex',
    desktopDownloadUrl:
      'https://claude.ai/api/desktop/win32/x64/setup/latest/redirect?utm_source=claude_code&utm_medium=docs',
    verifyCommand: 'claude --version',
    launchCommand: 'claude',
    diagnosticCommands: ['claude doctor'],
    officialSourceUrl: 'https://code.claude.com/docs/en/installation',
    verifiedAt: '2026-07-15'
  },
  {
    client: 'claude_code',
    os: 'linux',
    shell: 'terminal',
    installCommand: 'curl -fsSL https://claude.ai/install.sh | bash',
    verifyCommand: 'claude --version',
    launchCommand: 'claude',
    diagnosticCommands: ['claude doctor'],
    officialSourceUrl: 'https://code.claude.com/docs/en/installation',
    verifiedAt: '2026-07-15'
  },
  {
    client: 'codex',
    os: 'macos',
    shell: 'terminal',
    installCommand: 'curl -fsSL https://chatgpt.com/codex/install.sh | sh',
    verifyCommand: 'codex --version',
    launchCommand: 'codex',
    diagnosticCommands: ['codex login status', 'codex doctor'],
    officialSourceUrl: 'https://learn.chatgpt.com/docs/codex/cli',
    verifiedAt: '2026-07-15'
  },
  {
    client: 'codex',
    os: 'windows',
    shell: 'powershell',
    installCommand: 'npm install -g @openai/codex',
    desktopDownloadUrl:
      'https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi',
    verifyCommand: 'codex --version',
    launchCommand: 'codex',
    diagnosticCommands: ['codex login status', 'codex doctor'],
    officialSourceUrl: 'https://learn.chatgpt.com/docs/codex/cli',
    verifiedAt: '2026-07-15'
  },
  {
    client: 'codex',
    os: 'linux',
    shell: 'terminal',
    installCommand: 'curl -fsSL https://chatgpt.com/codex/install.sh | sh',
    verifyCommand: 'codex --version',
    launchCommand: 'codex',
    diagnosticCommands: ['codex login status', 'codex doctor'],
    officialSourceUrl: 'https://learn.chatgpt.com/docs/codex/cli',
    verifiedAt: '2026-07-15'
  }
]

export const GUIDE_CLIENT_IDS: BeginnerGuideClient[] = [
  ...new Set(GUIDE_VARIANTS.map(({ client }) => client))
]

export const GUIDE_OS_IDS: BeginnerGuideOS[] = [
  ...new Set(GUIDE_VARIANTS.map(({ os }) => os))
]
