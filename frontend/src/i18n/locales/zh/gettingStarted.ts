export default {
  gettingStarted: {
    title: '新手教程',
    discovery: {
      navLabel: '新手教程',
      eyebrow: '第一次使用 AI 工具？',
      title: '完全不懂也没关系，我们一步一步带你完成',
      description: '选择工具、安装客户端、连接本站，再完成你的第一次 AI 任务。',
      homeCta: '开始新手教程',
      stages: {
        choose: '选择工具',
        install: '安装客户端',
        connect: '连接本站',
        firstTask: '完成第一次任务'
      }
    },
    dashboard: {
      quickActionTitle: '新手教程',
      quickActionDescription: '配置 Claude Code 或 Codex，并完成你的第一次任务。',
      sidebarLabel: '新手教程'
    },
    welcome: {
      title: '让我们带你开始使用',
      description: '不需要任何 AI 使用经验。教程会带你完成每一步，你也可以随时回来继续。',
      start: '开始教程',
      closeLabel: '关闭新手教程欢迎窗口'
    },
    chrome: {
      guideLabel: '开始使用',
      clientSelector: '选择客户端',
      osSelector: '选择操作系统',
      progress: '教程进度',
      back: '上一步',
      next: '下一步',
      copy: '复制',
      copied: '已复制',
      copyFailed: '无法自动复制',
      manualCopy: '请选中文字后手动复制。',
      mobileStepMenu: '教程步骤',
      openStepMenu: '打开步骤菜单',
      closeStepMenu: '关闭步骤菜单'
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
        title: '认识这些工具',
        description: '先了解本教程会用到的五个简单概念。'
      },
      choose: {
        title: '选择客户端和系统',
        description: '选择你想使用的工具和电脑操作系统。'
      },
      terminal: {
        title: '打开终端',
        description: '找到电脑上已经安装的命令行应用。'
      },
      install: {
        title: '安装客户端',
        description: '运行官方安装程序，并确认客户端已经可以使用。'
      },
      api_key: {
        title: '选择 API 密钥',
        description: '需要时先登录，然后创建或选择一个兼容且已启用的密钥。'
      },
      configure: {
        title: '连接本站服务',
        description: '把生成的设置加入客户端，同时保留原有的其他设置。'
      },
      first_run: {
        title: '运行第一次任务',
        description: '重启客户端，发送一条安全的指令，再由你手动确认结果。'
      },
      troubleshoot: {
        title: '检查并排查问题',
        description: '如果第一次任务没有成功，请逐项检查常见原因。'
      }
    },
    definitions: {
      model: {
        title: 'AI 模型',
        description: '一种能够理解指令，并生成有用文字或代码的程序。'
      },
      agent: {
        title: 'AI 智能体',
        description: '在你的电脑上使用 AI 模型，帮助处理项目文件和任务的工具。'
      },
      terminal: {
        title: '终端',
        description: '一个输入命令并按回车后，让电脑执行操作的应用。'
      },
      gateway: {
        title: '网关',
        description: '本站通过一个账号和服务地址，把你的本地客户端连接到 AI 模型。'
      },
      apiKey: {
        title: 'API 密钥',
        description: '让所选客户端使用你账号的私密凭证。请像保护密码一样保护它。'
      }
    },
    terminal: {
      macos: {
        appName: '终端',
        openInstructions: '打开“聚焦搜索”，输入“终端”，再打开“终端”应用。'
      },
      windows: {
        appName: 'PowerShell',
        openInstructions: '打开“开始”菜单，输入 PowerShell，再打开 Windows PowerShell。'
      },
      linux: {
        appName: '终端应用',
        openInstructions: '打开应用菜单，选择“终端”“控制台”或桌面系统提供的终端应用。'
      },
      pasteAndRun: '把一条命令粘贴到终端，然后按回车运行。',
      normalOutput: '正常输出通常是几行文字，最后会再次出现可以继续输入的新提示符。'
    },
    installation: {
      explanation: '安装程序会下载官方客户端，并让你可以在终端中使用它的命令。',
      expectedResult: '安装过程没有报错，并且版本检查命令会显示版本号。',
      restartShell:
        '如果安装后提示找不到命令，请关闭当前终端，重新打开一个终端，再次运行版本检查。你的文件不会受到影响。',
      downloadDesktop: '下载桌面 App',
      cliFallback: '更习惯命令行？也可以使用下面的 CLI 命令：',
      officialSource: '打开官方安装说明'
    },
    apiKey: {
      anonymousTitle: '登录后继续',
      anonymousDescription:
        '安装步骤无需账号即可完成。现在登录或注册以创建或选择 API 密钥，完成后会回到这一步。',
      login: '登录',
      register: '注册',
      loading: '正在加载你的 API 密钥…',
      existingTitle: '选择一个已启用的 API 密钥',
      emptyTitle: '创建你的第一个 API 密钥',
      emptyDescription: '没有找到兼容且已启用的密钥。请在这里创建一个后继续。',
      create: '创建 API 密钥',
      inactive: '这个密钥尚未启用。',
      incompatible: '这个密钥与所选客户端不兼容。',
      secretWarning: 'API 密钥是私密信息。不要分享、粘贴到聊天中，或放入教程页面地址。'
    },
    configuration: {
      mergeWarning: '如果配置文件已经存在，请把这些设置合并进去，不要覆盖其他无关设置。',
      restartInstruction: '保存所有必需文件后，请完全关闭客户端和终端，再重新打开。',
      reselectAfterRefresh: '为了保护安全，所选密钥不会被保存。刷新页面后请重新选择。'
    },
    firstRun: {
      promptLabel: '安全的第一次指令',
      prompt: '请用三个简短要点说明这个项目的用途，不要修改任何文件。',
      restartInstruction: '关闭正在运行的客户端会话，打开一个新终端，再次启动客户端。',
      expectedResult: '成功时会得到相关说明，或被询问更多背景。具体文字可能不同。',
      confirmSuccess: '我收到了有用的回复'
    },
    troubleshooting: {
      version: '确认版本检查命令会显示版本号。',
      filePath: '确认每个配置文件都保存在页面显示的位置。',
      baseUrl: '确认配置中的服务地址与页面显示的地址完全一致。',
      restart: '关闭所有客户端和终端窗口，打开一个新终端后再试。',
      authentication: '如果身份验证失败，请确认所选 API 密钥已启用且与客户端兼容。',
      connection: '如果连接失败，请检查网络后重新运行命令。',
      shell: 'macOS 或 Linux 请使用终端，Windows 请使用 PowerShell，除非官方说明另有要求。',
      permissions: '如果出现权限不足，请先阅读官方说明，再更改系统权限。',
      officialSource: '对照最新官方说明',
      retry: '重试',
      retryLoading: '正在重试…'
    },
    completion: {
      title: '第一次配置已经完成',
      description: '以后需要再次检查配置时，可以随时回到这个教程。',
      dashboard: '前往仪表盘',
      keys: '管理 API 密钥',
      usage: '查看使用记录'
    },
    warnings: {
      networkAccess: '操作可能需要魔法梯子。',
      progressUnavailable: '无法读取已保存的教程进度。你仍可继续使用教程，稍后再重试。',
      progressSaveFailed: '最新进度无法保存到账号。教程仍可使用，请稍后重试。',
      promptSaveFailed: '无法把欢迎提示偏好保存到账号。当前已隐藏，系统稍后会重试。'
    }
  }
}
