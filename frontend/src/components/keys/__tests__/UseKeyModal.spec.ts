import { describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

// 代码块逐行渲染，每行一个 <code>；用 textContent 而非 text() 以保留缩进。
const codeBlocksOf = (wrapper: VueWrapper): string[] =>
  wrapper
    .findAll('[data-testid="code-block"]')
    .map((block) =>
      block
        .findAll('pre code')
        .map((code) => code.element.textContent ?? '')
        .join('\n')
    )

const { copyToClipboardMock } = vi.hoisted(() => ({
  copyToClipboardMock: vi.fn().mockResolvedValue(true)
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string) => key,
      locale: { value: 'en' }
    }
  }),
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: copyToClipboardMock
  })
}))

import UseKeyModal from '../UseKeyModal.vue'

describe('UseKeyModal', () => {
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

    const allCode = wrapper.findAll('pre code').map((code) => code.text()).join('\n')
    expect(allCode).toContain('GROK_MODELS_BASE_URL')
    expect(allCode).toContain('XAI_API_KEY')
    expect(allCode).toContain('[model."grok-4.5"]')
    expect(allCode).toContain('[model."grok-build-0.1"]')
    expect(allCode).toContain('[model."grok-4.20-multi-agent-0309"]')
    expect(allCode).toContain('[model."grok-4.3"]')
    expect(allCode).toContain('default = "grok-4.5"')
    expect(allCode).toContain('models_base_url = "https://example.com/v1"')
    expect(allCode).toContain('models_list_url = "https://example.com/v1/models"')
    expect(allCode).toContain('xai_api_base_url = "https://example.com/v1"')
    expect(allCode).toContain('cli_chat_proxy_base_url = "https://example.com/v1"')
    expect(allCode).toContain('preferred_method = "api_key"')
    expect(allCode).toContain('image_description = "grok-4.5"')
    expect(allCode).toContain('auto_compact_threshold_percent = 80')
    expect(allCode).toContain('image_gen = true')
    expect(allCode).toContain('video_gen = true')
    expect(allCode).toContain('image_gen_model_override = "grok-imagine-image-quality"')
    expect(allCode).toContain('image_edit_model_override = "grok-imagine-edit"')
    expect(allCode).toContain('env_key = "XAI_API_KEY"')
    expect(allCode).toContain('Keep api_backend = "responses" on every model entry.')
    expect(allCode).toContain('grok-imagine-image')
    expect(allCode).toContain('grok-imagine-edit')
    expect(allCode).toMatch(/\[model\."grok-4\.5"\][\s\S]*?context_window = 500000/)
    expect(allCode).toMatch(/\[model\."grok-build-0\.1"\][\s\S]*?context_window = 256000/)
    // Prefer env_key; hardcode api_key only as commented alternative
    expect(allCode).not.toMatch(/^api_key = "sk-grok-test"$/m)

    const modelBlocks = allCode
      .split(/(?=^\[model\.)/m)
      .filter((block) => block.startsWith('[model."'))
    expect(modelBlocks.length).toBeGreaterThanOrEqual(4)
    for (const block of modelBlocks) {
      if (block.includes('# [model.')) continue
      expect(block).toContain('api_backend = "responses"')
    }

    const windowsTab = wrapper.findAll('button').find(
      (button) => button.text().trim() === 'Windows'
    )
    expect(windowsTab).toBeDefined()
    await windowsTab!.trigger('click')
    await nextTick()
    expect(wrapper.text().toLowerCase()).toContain('%userprofile%\\.grok\\config.toml')

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )
    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const opencodeConfig = codeBlocksOf(wrapper).find((content) => content.includes('"provider"'))
    expect(opencodeConfig).toBeDefined()
    const parsed = JSON.parse(opencodeConfig!)
    expect(parsed.provider.grok.npm).toBe('@ai-sdk/openai-compatible')
    expect(parsed.provider.grok.name).toBe('Grok via RingStar')
    expect(parsed.provider.grok.options).toEqual({
      baseURL: 'https://example.com/v1',
      apiKey: 'sk-grok-test'
    })
    expect(parsed.provider.grok.models['grok-4.5']).toBeDefined()
    expect(parsed.provider.grok.models['grok-4.5'].limit.context).toBe(500000)
    expect(parsed.provider.grok.models['grok-build-0.1']).toBeDefined()
    expect(parsed.provider.grok.models['grok-4.20-multi-agent-0309']).toBeDefined()
    expect(parsed.provider.grok.models['grok-composer-2.5-fast']).toBeDefined()
    expect(parsed.provider.grok.models['gpt-5.6']).toBeUndefined()
  })

  it('renders copyable Claude Code setup through the Grok Messages gateway', async () => {
    copyToClipboardMock.mockClear()
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-grok-claude-test',
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

    const claudeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.claudeCode')
    )
    expect(claudeTab).toBeDefined()
    await claudeTab!.trigger('click')
    await nextTick()

    let codeBlocks = codeBlocksOf(wrapper)
    expect(codeBlocks.join('\n')).toContain('ANTHROPIC_BASE_URL="https://example.com"')
    expect(codeBlocks.join('\n')).toContain('ANTHROPIC_AUTH_TOKEN="sk-grok-claude-test"')
    const unixConfig = codeBlocks.find((content) => content.startsWith('export ANTHROPIC_BASE_URL'))
    expect(unixConfig).toBeDefined()
    for (const name of [
      'ANTHROPIC_MODEL',
      'ANTHROPIC_DEFAULT_OPUS_MODEL',
      'ANTHROPIC_DEFAULT_SONNET_MODEL',
      'ANTHROPIC_DEFAULT_HAIKU_MODEL',
      'ANTHROPIC_DEFAULT_FABLE_MODEL',
      'CLAUDE_CODE_SUBAGENT_MODEL'
    ]) {
      expect(unixConfig).toContain(`export ${name}="grok-4.5"`)
    }
    const settingsConfig = codeBlocks.find((content) => content.includes('"$schema"'))
    expect(settingsConfig).toBeDefined()
    const parsedSettings = JSON.parse(settingsConfig!)
    expect(parsedSettings.$schema).toBe('https://json.schemastore.org/claude-code-settings.json')
    expect(parsedSettings.env.ANTHROPIC_MODEL).toBe('grok-4.5')
    expect(wrapper.text()).toContain('keys.useKeyModal.claudeSettingsHint')
    expect(wrapper.text()).toContain('keys.useKeyModal.grok.claudeNote')
    expect(wrapper.find('nav[aria-label="Client"]').classes()).toContain('min-w-max')
    expect(wrapper.find('nav[aria-label="Client"]').element.parentElement?.classList.contains('overflow-x-auto')).toBe(true)

    const cmdTab = wrapper.findAll('button').find(
      (button) => button.text().trim() === 'Windows CMD'
    )
    expect(cmdTab).toBeDefined()
    await cmdTab!.trigger('click')
    await nextTick()

    codeBlocks = codeBlocksOf(wrapper)
    expect(codeBlocks.join('\n')).toContain('set ANTHROPIC_MODEL=grok-4.5')
    expect(codeBlocks.join('\n')).toContain('set ANTHROPIC_DEFAULT_FABLE_MODEL=grok-4.5')
    expect(codeBlocks.join('\n')).toContain('set CLAUDE_CODE_SUBAGENT_MODEL=grok-4.5')

    const powershellTab = wrapper.findAll('button').find(
      (button) => button.text().trim() === 'PowerShell'
    )
    expect(powershellTab).toBeDefined()
    await powershellTab!.trigger('click')
    await nextTick()

    codeBlocks = codeBlocksOf(wrapper)
    expect(codeBlocks.join('\n')).toContain('$env:ANTHROPIC_BASE_URL="https://example.com"')
    expect(codeBlocks.join('\n')).toContain('$env:ANTHROPIC_MODEL="grok-4.5"')
    expect(codeBlocks.join('\n')).toContain('$env:ANTHROPIC_DEFAULT_FABLE_MODEL="grok-4.5"')
    expect(codeBlocks.join('\n')).toContain('$env:CLAUDE_CODE_SUBAGENT_MODEL="grok-4.5"')
    expect(wrapper.text()).toContain('%USERPROFILE%\\.claude\\settings.json')

    const copyButton = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.copy')
    )
    expect(copyButton).toBeDefined()
    await copyButton!.trigger('click')
    expect(copyToClipboardMock).toHaveBeenCalledWith(
      expect.stringContaining('ANTHROPIC_AUTH_TOKEN="sk-grok-claude-test"'),
      'keys.copied'
    )
  })

  it('renders Codex custom provider setup through the Grok Responses gateway', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-grok-codex-test',
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

    const codexTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.codexCli')
    )
    expect(codexTab).toBeDefined()
    await codexTab!.trigger('click')
    await nextTick()

    let codeBlocks = codeBlocksOf(wrapper)
    const configToml = codeBlocks.find((content) => content.includes('[model_providers.sub2api]'))
    expect(configToml).toBeDefined()
    expect(configToml).toContain('model_provider = "sub2api"')
    expect(configToml).toContain('model = "grok-4.5"')
    expect(configToml).toContain('base_url = "https://example.com/v1"')
    expect(configToml).toContain('env_key = "SUB2API_API_KEY"')
    expect(configToml).toContain('wire_api = "responses"')
    // API-key provider: Codex must not require a ChatGPT OAuth login.
    expect(configToml).toContain('requires_openai_auth = false')
    expect(configToml).toContain('supports_websockets = false')
    expect(configToml).toContain('grok-4.20-multi-agent-0309 (text / web_search)')
    expect(configToml).toContain('grok-imagine-image')
    expect(configToml).toContain('grok-imagine-video')
    // Hardcoded bearer is only a commented fallback when env cannot be set.
    expect(configToml).toMatch(/# experimental_bearer_token = "sk-grok-codex-test"/)
    expect(configToml).not.toContain('supports_websockets = true')
    expect(configToml).not.toContain('responses_websockets_v2')
    expect(wrapper.text()).not.toContain('auth.json')
    expect(codeBlocks.join('\n')).toContain('SUB2API_API_KEY')

    const windowsTab = wrapper.findAll('button').find(
      (button) => button.text().trim() === 'Windows'
    )
    expect(windowsTab).toBeDefined()
    await windowsTab!.trigger('click')
    await nextTick()

    codeBlocks = codeBlocksOf(wrapper)
    expect(wrapper.text().toLowerCase()).toContain('%userprofile%\\.codex\\config.toml'.toLowerCase())
    expect(codeBlocks.join('\n')).toContain('experimental_bearer_token = "sk-grok-codex-test"')
  })

  it('keeps legacy OpenAI Codex config as the default', () => {
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

    const codeBlocks = codeBlocksOf(wrapper)
    const configToml = codeBlocks.find((content) => content.includes('model_provider = "OpenAI"'))

    expect(configToml).toBeDefined()
    expect(configToml).toContain('model = "gpt-5.5"')
    expect(configToml).toContain('review_model = "gpt-5.5"')
    expect(configToml).not.toContain('model = "gpt-5.4"')
    expect(configToml).not.toContain('model_context_window')
    expect(configToml).not.toContain('model_auto_compact_token_limit')
    expect(configToml).toContain('requires_openai_auth = true')
    expect(configToml).not.toContain('x-openai-actor-authorization')
    expect(configToml).not.toContain('env_key')
    expect(configToml).not.toContain('image_generation')
    expect(configToml).not.toContain('supports_websockets')
    expect(configToml).not.toContain('responses_websockets_v2')
    expect(configToml).toContain('[features]\ngoals = true')
    expect(codeBlocks).toContain('{\n  "OPENAI_API_KEY": "sk-test"\n}')
    expect(wrapper.text()).toContain('auth.json')
    expect(wrapper.find('[data-testid="codex-api-key-restart-notice"]').exists()).toBe(false)
  })

  it('renders API Key Mode authorization in OpenAI Codex config', async () => {
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

    const apiKeyMode = wrapper.get('[data-testid="codex-auth-mode-api-key"]')
    await apiKeyMode.trigger('click')
    await nextTick()

    const codeBlocks = codeBlocksOf(wrapper)
    const configToml = codeBlocks.find((content) => content.includes('model_provider = "OpenAI"'))

    expect(apiKeyMode.attributes('aria-checked')).toBe('true')
    expect(configToml).toBeDefined()
    expect(configToml).toContain('requires_openai_auth = false')
    expect(configToml).toContain('http_headers = { "x-openai-actor-authorization" = "local-image-extension" }')
    expect(configToml).not.toContain('env_key')
    expect(configToml).not.toContain('image_generation')
    expect(codeBlocks).toContain('{\n  "OPENAI_API_KEY": "sk-test"\n}')
    expect(wrapper.text()).toContain('auth.json')

    const restartNotice = wrapper.get('[data-testid="codex-api-key-restart-notice"]')
    expect(restartNotice.text()).toContain(
      'keys.useKeyModal.openai.authModeApiKeyRestartNotice'
    )

    await wrapper.get('[data-testid="codex-auth-mode-legacy"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-testid="codex-api-key-restart-notice"]').exists()).toBe(false)
    expect(codeBlocksOf(wrapper).join('\n')).not.toContain(
      'x-openai-actor-authorization'
    )
  })

  it('keeps legacy OpenAI Codex WebSocket config as the default', async () => {
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

    const wsTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.codexCliWs')
    )

    expect(wsTab).toBeDefined()
    await wsTab!.trigger('click')
    await nextTick()

    const codeBlocks = codeBlocksOf(wrapper)
    const configToml = codeBlocks.find((content) => content.includes('supports_websockets = true'))

    expect(configToml).toBeDefined()
    expect(configToml).toContain('model = "gpt-5.5"')
    expect(configToml).toContain('review_model = "gpt-5.5"')
    expect(configToml).not.toContain('model = "gpt-5.4"')
    expect(configToml).not.toContain('model_context_window')
    expect(configToml).not.toContain('model_auto_compact_token_limit')
    expect(configToml).toContain('requires_openai_auth = true')
    expect(configToml).not.toContain('x-openai-actor-authorization')
    expect(configToml).not.toContain('env_key')
    expect(configToml).not.toContain('image_generation')
    expect(configToml).toContain('supports_websockets = true')
    expect(configToml).toContain('[features]\nresponses_websockets_v2 = true\ngoals = true')
    expect(codeBlocks).toContain('{\n  "OPENAI_API_KEY": "sk-test"\n}')
    expect(wrapper.text()).toContain('auth.json')
  })

  it('preserves API Key Mode when switching to OpenAI Codex WebSocket config', async () => {
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

    const apiKeyMode = wrapper.get('[data-testid="codex-auth-mode-api-key"]')
    await apiKeyMode.trigger('click')

    const wsTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.codexCliWs')
    )
    expect(wsTab).toBeDefined()
    await wsTab!.trigger('click')
    await nextTick()

    const codeBlocks = codeBlocksOf(wrapper)
    const configToml = codeBlocks.find((content) => content.includes('supports_websockets = true'))

    expect(wrapper.get('[data-testid="codex-auth-mode-api-key"]').attributes('aria-checked')).toBe('true')
    expect(configToml).toBeDefined()
    expect(configToml).toContain('requires_openai_auth = false')
    expect(configToml).toContain('http_headers = { "x-openai-actor-authorization" = "local-image-extension" }')
    expect(configToml).not.toContain('env_key')
    expect(configToml).not.toContain('image_generation')
    expect(configToml).toContain('supports_websockets = true')
    expect(configToml).toContain('[features]\nresponses_websockets_v2 = true\ngoals = true')
    expect(codeBlocks).toContain('{\n  "OPENAI_API_KEY": "sk-test"\n}')
  })

  it('resets Codex authentication mode when the modal reopens or platform changes', async () => {
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

    await wrapper.get('[data-testid="codex-auth-mode-api-key"]').trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await nextTick()

    expect(wrapper.get('[data-testid="codex-auth-mode-legacy"]').attributes('aria-checked')).toBe('true')
    expect(codeBlocksOf(wrapper).join('\n')).toContain('requires_openai_auth = true')

    await wrapper.get('[data-testid="codex-auth-mode-api-key"]').trigger('click')
    await wrapper.setProps({ platform: 'gemini' })
    await wrapper.setProps({ platform: 'openai' })
    await nextTick()

    expect(wrapper.get('[data-testid="codex-auth-mode-legacy"]').attributes('aria-checked')).toBe('true')
    expect(codeBlocksOf(wrapper).join('\n')).not.toContain('x-openai-actor-authorization')
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

    const codeBlock = codeBlocksOf(wrapper)[0]
    expect(codeBlock).toBeDefined()
    expect(codeBlock).toContain('"name": "GPT-5.4 Mini"')
    expect(codeBlock).not.toContain('"name": "GPT-5.4 Nano"')
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

    const parsed = JSON.parse(codeBlocksOf(wrapper)[0])
    const models = parsed.provider.openai.models
    for (const model of ['gpt-5.6', 'gpt-5.6-sol', 'gpt-5.6-sol-wm', 'gpt-5.6-terra', 'gpt-5.6-luna']) {
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

    const claudeConfig = codeBlocksOf(wrapper)
      .find((content) => content.includes('"antigravity-claude"'))

    expect(claudeConfig).toBeDefined()
    const parsed = JSON.parse(claudeConfig!)
    const fable = parsed.provider['antigravity-claude'].models['claude-fable-5']

    expect(fable.name).toBe('Claude Fable 5')
    expect(fable.limit).toEqual({ context: 1048576, output: 128000 })
    expect(fable.options.thinking).toEqual({ type: 'adaptive' })
    expect(fable.options.thinking).not.toHaveProperty('budgetTokens')
  })

  it('renders Cursor IDE setup pointing at the tool-free /cursor-ide/v1 endpoint', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-cursor-ide-test',
        baseUrl: 'https://example.com/v1',
        platform: 'cursor'
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

    const ideTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.cursorIde')
    )
    expect(ideTab).toBeDefined()
    await ideTab!.trigger('click')
    await nextTick()

    const config = codeBlocksOf(wrapper).find((content) => content.includes('Override OpenAI Base URL'))
    expect(config).toBeDefined()
    // 前缀写错就等于把工具声明重新带上去，所以这里钉死完整地址。
    expect(config).toContain('Override OpenAI Base URL: https://example.com/cursor-ide/v1')
    expect(config).toContain('OpenAI API Key: sk-cursor-ide-test')
    expect(config).toContain('Model: cursor/default')

    // 这一档是在图形界面里填表，shell 页签不该出现。
    const shellTab = wrapper.findAll('button').find((button) => button.text().includes('PowerShell'))
    expect(shellTab).toBeUndefined()
  })
})
