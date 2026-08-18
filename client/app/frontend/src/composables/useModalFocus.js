import {watch, nextTick, onBeforeUnmount} from 'vue'

// useModalFocus — 轻量弹窗焦点陷阱（无障碍）。
// activeRef 为真时：聚焦容器内首个可聚焦元素，Tab 循环锁定在容器内；为假时释放。
// 用法：const container = ref(null); useModalFocus(container, computed(() => !!show))
const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'

export function useModalFocus(containerRef, activeRef) {
  let unbind = null

  function release() {
    if (unbind) {
      unbind()
      unbind = null
    }
  }

  function trap(e) {
    if (e.key !== 'Tab' || !containerRef.value) return
    const list = containerRef.value.querySelectorAll(FOCUSABLE)
    if (!list.length) return
    const first = list[0]
    const last = list[list.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  function bind() {
    release()
    const el = containerRef.value
    if (!el) return
    document.addEventListener('keydown', trap, true)
    unbind = () => document.removeEventListener('keydown', trap, true)
    const focusables = el.querySelectorAll(FOCUSABLE)
    if (focusables.length) focusables[0].focus()
  }

  watch(activeRef, (v) => {
    if (v) nextTick(bind)
    else release()
  }, {immediate: true})

  onBeforeUnmount(release)

  return {release}
}
