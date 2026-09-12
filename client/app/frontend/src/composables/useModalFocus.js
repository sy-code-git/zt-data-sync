import {watch, nextTick, onBeforeUnmount} from 'vue'

// useModalFocus — 轻量弹窗焦点陷阱（无障碍）。
// activeRef 为真时：聚焦容器内首个可聚焦元素，Tab 循环锁定在容器内；为假时释放。
// 多弹窗并发（如列表弹窗 + 列配置面板同开）时按注册顺序做栈管理：仅栈顶弹窗持有陷阱，
// 关闭后自动回退到前一个弹窗，避免多个实例互相抢焦点。
// 用法：const container = ref(null); useModalFocus(container, computed(() => !!show))
const FOCUSABLE = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'

// 模块级注册表：所有弹窗实例按激活顺序入栈
const activeStack = []

function bindTop(handle) {
  const top = activeStack[activeStack.length - 1]
  if (top !== handle || !handle.el || handle.bound) return
  // 新栈顶生效前，先释放其余已绑实例（保证同一时刻只有一个陷阱在监听）
  for (const h of activeStack) {
    if (h !== handle) releaseHandle(h)
  }
  handle.bound = true
  document.addEventListener('keydown', handle.trap, true)
  const focusables = handle.el.querySelectorAll(FOCUSABLE)
  if (focusables.length) focusables[0].focus()
}

function releaseHandle(handle) {
  if (handle.bound) {
    document.removeEventListener('keydown', handle.trap, true)
    handle.bound = false
  }
}

function rebindTop() {
  const top = activeStack[activeStack.length - 1]
  if (top) bindTop(top)
}

export function useModalFocus(containerRef, activeRef) {
  const handle = {
    el: null,
    trap: null,
    bound: false,
  }

  handle.trap = (e) => {
    if (e.key !== 'Tab' || !handle.el) return
    const list = handle.el.querySelectorAll(FOCUSABLE)
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

  function activate() {
    handle.el = containerRef.value
    handle.bound = false
    if (!handle.el) return
    // 栈内去重（同一实例重复激活时不重复入栈）
    if (!activeStack.includes(handle)) activeStack.push(handle)
    nextTick(() => {
      handle.el = containerRef.value
      bindTop(handle)
    })
  }

  function deactivate() {
    const i = activeStack.indexOf(handle)
    if (i >= 0) activeStack.splice(i, 1)
    releaseHandle(handle)
    handle.el = null
    nextTick(rebindTop) // 回退到前一个弹窗
  }

  watch(activeRef, (v) => {
    if (v) activate()
    else deactivate()
  }, {immediate: true})

  onBeforeUnmount(() => {
    deactivate()
  })

  return {release: deactivate}
}
