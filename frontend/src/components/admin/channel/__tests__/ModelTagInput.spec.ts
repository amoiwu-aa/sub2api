import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ModelTagInput from '../ModelTagInput.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

function mountInput(models = ['gpt-4', 'gpt-5', 'gpt-image-1']) {
  return mount(ModelTagInput, {
    props: { models, platform: 'openai' },
    global: {
      stubs: {
        Icon: true,
      },
    },
  })
}

describe('ModelTagInput', () => {
  it('可以点击任意标签的删除按钮，而不是只能删除最后一个', async () => {
    const wrapper = mountInput()
    const middleTag = wrapper.get('[data-model-index="1"]')

    await middleTag.get('button').trigger('click')

    expect(wrapper.emitted('update:models')?.[0]).toEqual([
      ['gpt-4', 'gpt-image-1'],
    ])
  })

  it('批量文本编辑允许在列表任意位置修改、删除和添加模型', async () => {
    const wrapper = mountInput()

    await wrapper.get('[data-testid="model-bulk-edit"]').trigger('click')
    const textarea = wrapper.get('[data-testid="model-bulk-textarea"]')
    await textarea.setValue('gpt-4\ngpt-image-1\ngpt-5.6-sol\ngpt-4')
    await wrapper.get('[data-testid="model-bulk-apply"]').trigger('click')

    expect(wrapper.emitted('update:models')?.[0]).toEqual([
      ['gpt-4', 'gpt-image-1', 'gpt-5.6-sol'],
    ])
  })
})
