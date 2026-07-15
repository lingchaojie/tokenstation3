export default {
  gettingStarted: {
    title: 'Beginner Guide',
    discovery: {
      navLabel: 'Beginner Guide',
      eyebrow: 'New to AI tools?',
      title: 'No prior knowledge needed. We will guide you step by step.',
      description:
        'Choose a tool, install it, connect this service, and complete your first AI task.',
      homeCta: 'Start beginner guide',
      stages: {
        choose: 'Choose a tool',
        install: 'Install the client',
        connect: 'Connect this service',
        firstTask: 'Complete your first task'
      }
    },
    dashboard: {
      quickActionTitle: 'Beginner Guide',
      quickActionDescription: 'Set up Claude Code or Codex and complete your first task.',
      sidebarLabel: 'Beginner Guide'
    },
    welcome: {
      title: 'Let us help you get started',
      description:
        'You do not need any AI experience. The guide walks you through every step and you can return at any time.',
      start: 'Start guide',
      closeLabel: 'Close beginner guide welcome'
    },
    chrome: {
      guideLabel: 'Getting started',
      clientSelector: 'Choose your client',
      osSelector: 'Choose your operating system',
      progress: 'Guide progress',
      back: 'Back',
      next: 'Next',
      copy: 'Copy',
      copied: 'Copied',
      copyFailed: 'Could not copy automatically',
      manualCopy: 'Select the text and copy it manually.',
      mobileStepMenu: 'Guide steps',
      openStepMenu: 'Open step menu',
      closeStepMenu: 'Close step menu'
    },
    clients: {
      claude_code: 'Claude Code',
      codex: 'Codex'
    },
    operatingSystems: {
      macos: 'macOS',
      windows: 'Windows',
      linux: 'Linux'
    },
    steps: {
      understand: {
        title: 'Understand the tools',
        description: 'Learn the five simple terms used throughout this guide.'
      },
      choose: {
        title: 'Choose a client and system',
        description: 'Choose the tool and operating system you want to use.'
      },
      terminal: {
        title: 'Open a terminal',
        description: 'Find the command-line app already available on your computer.'
      },
      install: {
        title: 'Install the client',
        description: 'Run the official installer and confirm that the client is available.'
      },
      api_key: {
        title: 'Choose an API key',
        description: 'Sign in when needed, then create or select a compatible active key.'
      },
      configure: {
        title: 'Connect to this service',
        description: 'Add the generated settings to your client without replacing unrelated settings.'
      },
      first_run: {
        title: 'Run your first task',
        description: 'Restart the client, send a harmless prompt, and confirm the result yourself.'
      },
      troubleshoot: {
        title: 'Verify and troubleshoot',
        description: 'Check the common causes if the first task did not work.'
      }
    },
    definitions: {
      model: {
        title: 'AI model',
        description: 'A program that understands instructions and produces useful text or code.'
      },
      agent: {
        title: 'AI agent',
        description:
          'A tool on your computer that uses an AI model to help with files and tasks in your project.'
      },
      terminal: {
        title: 'Terminal',
        description: 'An app where you type a command and press Enter to ask your computer to do it.'
      },
      gateway: {
        title: 'Gateway',
        description:
          'This service connects your local client to an AI model through one account and address.'
      },
      apiKey: {
        title: 'API key',
        description:
          'A private credential that lets the selected client use your account. Treat it like a password.'
      }
    },
    terminal: {
      macos: {
        appName: 'Terminal',
        openInstructions: 'Open Spotlight, type Terminal, and open the Terminal app.'
      },
      windows: {
        appName: 'PowerShell',
        openInstructions: 'Open Start, type PowerShell, and open Windows PowerShell.'
      },
      linux: {
        appName: 'Terminal app',
        openInstructions:
          'Open your applications menu and choose Terminal, Console, or the terminal app provided by your desktop.'
      },
      pasteAndRun: 'Paste one command into the terminal, then press Enter to run it.',
      normalOutput:
        'Normal output is one or more lines of text followed by a new prompt where you can type again.'
    },
    installation: {
      explanation:
        'The installer downloads the official client and makes its command available in your terminal.',
      expectedResult:
        'The installation finishes without an error and the version command prints a version number.',
      restartShell:
        'If the command is not found after installation, close this terminal, open a new one, and run the version check again. Your files are not affected.',
      officialSource: 'Open the official installation instructions'
    },
    apiKey: {
      anonymousTitle: 'Sign in to continue',
      anonymousDescription:
        'Installation can be completed without an account. Sign in or register now to create or select an API key, then return to this step.',
      login: 'Sign in',
      register: 'Register',
      loading: 'Loading your API keys…',
      existingTitle: 'Choose an active API key',
      emptyTitle: 'Create your first API key',
      emptyDescription: 'No compatible active key was found. Create one here to continue.',
      create: 'Create API key',
      inactive: 'This key is inactive.',
      incompatible: 'This key is not compatible with the selected client.',
      secretWarning:
        'Your API key is secret. Do not share it, paste it into chat, or store it in the guide address.'
    },
    configuration: {
      mergeWarning:
        'If a configuration file already exists, merge these settings into it. Do not replace unrelated settings.',
      restartInstruction:
        'After saving every required file, fully close the client and terminal, then open them again.',
      reselectAfterRefresh:
        'For your security, the selected key is not saved. Choose it again after refreshing this page.'
    },
    firstRun: {
      promptLabel: 'Safe first prompt',
      prompt: 'Explain the purpose of this project in three short bullet points. Do not change any files.',
      restartInstruction: 'Close any running client session, open a new terminal, and launch the client again.',
      expectedResult:
        'A successful result is a relevant explanation or a request for more context. The exact wording may differ.',
      confirmSuccess: 'I received a useful response'
    },
    troubleshooting: {
      version: 'Confirm that the version command prints a version number.',
      filePath: 'Confirm that each configuration file was saved at the displayed path.',
      baseUrl: 'Confirm that the configured service address exactly matches the displayed address.',
      restart: 'Close every client and terminal window, then open a new terminal and try again.',
      authentication: 'If authentication fails, confirm that the selected API key is active and compatible.',
      connection: 'If the connection fails, check your network and retry the command.',
      shell: 'Use Terminal on macOS or Linux and PowerShell on Windows unless the official guide says otherwise.',
      permissions: 'If permission is denied, read the official instructions before changing system permissions.',
      officialSource: 'Compare with the current official instructions',
      retry: 'Try again',
      retryLoading: 'Trying again…'
    },
    completion: {
      title: 'Your first setup is complete',
      description: 'You can return to this guide whenever you need to check the setup again.',
      dashboard: 'Go to Dashboard',
      keys: 'Manage API Keys',
      usage: 'View Usage'
    },
    warnings: {
      progressUnavailable:
        'The saved guide progress could not be loaded. You can keep using the guide and retry later.',
      progressSaveFailed:
        'Your latest progress could not be saved to your account. The guide remains usable; please retry later.',
      promptSaveFailed:
        'The welcome preference could not be saved to your account. It is hidden for now and will be retried later.'
    }
  }
}
