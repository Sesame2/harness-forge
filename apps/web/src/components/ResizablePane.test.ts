import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ResizablePane from './ResizablePane.vue'

function pane() {
  return mount(ResizablePane, {
    props: { modelValue: 440, min: 320, max: 640, label: '对话栏宽度', paneId: 'chat' },
    slots: { default: '<p>Conversation</p>' },
  })
}

describe('ResizablePane', () => {
  it('labels a focusable vertical separator with pixel bounds and its controlled pane', () => {
    const wrapper = pane()
    const handle = wrapper.get('[role="separator"]')
    expect(handle.attributes()).toMatchObject({
      tabindex: '0', 'aria-label': '对话栏宽度', 'aria-orientation': 'vertical',
      'aria-valuemin': '320', 'aria-valuemax': '640', 'aria-valuenow': '440', 'aria-controls': 'chat',
    })
    expect(wrapper.get('#chat').text()).toBe('Conversation')
    wrapper.unmount()
  })

  it('resizes with arrows and Home/End, clamps the value, and ignores other keys', async () => {
    const wrapper = pane()
    const handle = wrapper.get('[role="separator"]')
    for (const key of ['ArrowRight', 'ArrowLeft', 'Home', 'End']) await handle.trigger('keydown', { key })
    expect(wrapper.emitted('update:modelValue')).toEqual([[456], [424], [320], [640]])
    await wrapper.setProps({ modelValue: 640 })
    await handle.trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([640])
    await handle.trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('update:modelValue')).toHaveLength(5)
    wrapper.unmount()
  })

  it('drags by pointer delta and stops listening after release or unmount', async () => {
    const wrapper = pane()
    wrapper.get('[role="separator"]').element.dispatchEvent(new MouseEvent('pointerdown', { clientX: 100, button: 0 }))
    window.dispatchEvent(new MouseEvent('pointermove', { clientX: 150 }))
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([490])
    window.dispatchEvent(new MouseEvent('pointermove', { clientX: 900 }))
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([640])
    window.dispatchEvent(new MouseEvent('pointerup'))
    window.dispatchEvent(new MouseEvent('pointermove', { clientX: 200 }))
    expect(wrapper.emitted('update:modelValue')).toHaveLength(2)
    const removeListener = vi.spyOn(window, 'removeEventListener')
    wrapper.unmount()
    expect(removeListener).toHaveBeenCalledWith('pointermove', expect.any(Function))
    removeListener.mockRestore()
  })
})
