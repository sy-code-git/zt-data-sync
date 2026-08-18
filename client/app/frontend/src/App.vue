<script setup>
import {onMounted, onBeforeUnmount, ref, computed} from 'vue'
import {storeToRefs} from 'pinia'
import {
  WindowMinimise, WindowToggleMaximise, Quit,
  EventsOn,
} from '../wailsjs/runtime/runtime'
import {useAppStore} from './store'
import {useModalFocus} from './composables/useModalFocus'
import UnlockView from './views/UnlockView.vue'
import ListView from './views/ListView.vue'
import EditView from './views/EditView.vue'
import ConflictView from './views/ConflictView.vue'
import SettingsView from './views/SettingsView.vue'
import AdminView from './views/AdminView.vue'

const store = useAppStore()
const {view, booting, toasts, theme} = storeToRefs(store)

// 删除确认弹窗焦点陷阱
const deleteModalRef = ref(null)
useModalFocus(deleteModalRef, computed(() => !!store.deleteConfirm))

// 失焦连续 5 分钟自动锁定并清空内存密钥（§4/§5 自动锁定约定）
const AUTO_LOCK_MS = 5 * 60 * 1000
let blurTimer = null
const lockCountdown = ref(0) // 剩余秒数（失焦倒计时提示，§9.2）
let countdownTick = null

const stopCountdown = () => {
  if (countdownTick) clearInterval(countdownTick)
  countdownTick = null
  lockCountdown.value = 0
}
const startCountdown = () => {
  stopCountdown()
  lockCountdown.value = Math.ceil(AUTO_LOCK_MS / 1000)
  countdownTick = setInterval(() => {
    lockCountdown.value -= 1
    if (lockCountdown.value <= 0) stopCountdown()
  }, 1000)
}

const onBlur = () => {
  clearTimeout(blurTimer)
  blurTimer = setTimeout(() => {
    if (store.unlocked) store.lock()
  }, AUTO_LOCK_MS)
  if (store.unlocked) startCountdown()
}
const onFocus = () => {
  clearTimeout(blurTimer)
  stopCountdown()
}

function onWinEvent(ev) {
  if (ev.type === 'entries_changed') store.refreshEntries()
  if (ev.type === 'sync_status' || ev.type === 'rekey_started' || ev.type === 'rekey_done') {
    store.refreshStatus()
  }
  if (ev.type === 'locked') {
    store.unlocked = false
    store.entries = []
    store.view = 'unlock'
  }
  // token 彻底失效（被吊销/作废）→ 回解锁页并提示重新解锁
  if (ev.type === 'auth_expired') {
    store.unlocked = false
    store.entries = []
    store.view = 'unlock'
    store.toast(String(ev.data || '登录已失效，请重新解锁'), 'error')
  }
  if (ev.type === 'error') {
    store.toast(String(ev.data || '同步异常'), 'error')
  }
}

// Esc 关闭删除确认弹窗（无障碍）
const onKeydown = (e) => {
  if (e.key === 'Escape' && store.deleteConfirm) {
    store.deleteConfirm = null
  }
}

onMounted(async () => {
  // 订阅 core 事件（Wails 环境；浏览器预览跳过）
  if (window.go) {
    EventsOn('core:event', onWinEvent)
  }
  await store.bootstrap()
  window.addEventListener('blur', onBlur)
  window.addEventListener('focus', onFocus)
  document.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  clearTimeout(blurTimer)
  stopCountdown()
  window.removeEventListener('blur', onBlur)
  window.removeEventListener('focus', onFocus)
  document.removeEventListener('keydown', onKeydown)
})

const countdownText = computed(() => {
  if (!lockCountdown.value) return ''
  const m = Math.floor(lockCountdown.value / 60)
  const s = lockCountdown.value % 60
  return `${m}:${String(s).padStart(2, '0')} 后自动锁定`
})

const currentView = computed(() => {
  switch (view.value) {
    case 'unlock': return UnlockView
    case 'list': return ListView
    case 'edit': return EditView
    case 'conflict': return ConflictView
    case 'settings': return SettingsView
    case 'admin': return AdminView
    default: return UnlockView
  }
})
</script>

<template>
  <div class="shell" :class="`shell--${theme}`">
    <!-- 背景光晕（科技感） -->
    <div class="bg-glow bg-glow--a"></div>
    <div class="bg-glow bg-glow--b"></div>
    <div class="bg-grid"></div>

    <!-- 自绘标题栏（frameless） -->
    <header class="titlebar">
      <div class="titlebar__brand">
        <span class="titlebar__logo">&#128274;</span>
        <span class="titlebar__name">在线密码本</span>
        <span class="titlebar__tag">Zero-Knowledge</span>
      </div>
      <div class="titlebar__drag"></div>
      <div class="titlebar__controls">
        <button class="titlebar__btn" title="切换主题" @click="store.toggleTheme">
          {{ theme === 'dark' ? '☀' : '☾' }}
        </button>
        <button class="titlebar__btn" title="最小化" @click="WindowMinimise">–</button>
        <button class="titlebar__btn" title="最大化" @click="WindowToggleMaximise">□</button>
        <button class="titlebar__btn titlebar__btn--close" title="退出" @click="Quit">✕</button>
      </div>
    </header>

    <!-- 失焦自动锁定倒计时提示（§9.2） -->
    <div v-if="countdownText" class="autolock-bar">
      <span class="pb-dot pb-dot--warn"></span>
      {{ countdownText }}
    </div>

    <!-- 主区域 -->
    <main class="stage">
      <div v-if="booting" class="stage-boot">
        <div class="pb-spinner" style="width:28px;height:28px"></div>
        <p>正在安全启动…</p>
      </div>
      <transition v-else name="view" mode="out-in">
        <component :is="currentView" :key="view"/>
      </transition>
    </main>

    <!-- Toast 容器 -->
    <div class="pb-toast-wrap">
      <div v-for="t in toasts" :key="t.id" class="pb-toast" :class="`pb-toast--${t.type}`">
        <span class="pb-toast__icon">
          {{ t.type === 'success' ? '✓' : t.type === 'error' ? '✕' : t.type === 'warn' ? '!' : 'ℹ' }}
        </span>
        <span>{{ t.text }}</span>
      </div>
    </div>

    <!-- 删除确认弹窗 -->
    <div v-if="store.deleteConfirm" class="pb-modal-mask">
      <div ref="deleteModalRef" class="pb-modal pb-glass pb-glass--strong" role="dialog" aria-modal="true" aria-label="确认删除">
        <div class="pb-modal__head">
          <span class="pb-modal__title">确认删除</span>
          <button class="pb-iconbtn" @click="store.deleteConfirm = null">✕</button>
        </div>
        <div class="pb-modal__body">
          <p>确定删除条目 <b class="pb-mono">{{ store.deleteConfirm.title }}</b> 吗？</p>
          <p v-if="store.deleteConfirm.subtree_count > 1" class="pb-sm pb-muted">
            将连同其下 {{ store.deleteConfirm.subtree_count - 1 }} 条子条目一并删除。
          </p>
          <p class="pb-sm pb-muted">删除后将同步到所有成员；如需恢复可在服务端回收站查看（本地保留 30 天）。</p>
        </div>
        <div class="pb-modal__foot">
          <button class="pb-btn pb-btn--ghost" @click="store.deleteConfirm = null">取消</button>
          <button class="pb-btn pb-btn--danger" @click="store.confirmDelete()">确认删除</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.shell {
  position: relative;
  height: 100vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: linear-gradient(160deg, var(--bg-grad-a) 0%, var(--bg-grad-b) 48%, var(--bg-grad-c) 100%);
}

/* 背景光晕 */
.bg-glow {
  position: absolute;
  border-radius: 50%;
  filter: blur(90px);
  pointer-events: none;
  z-index: 0;
}
.bg-glow--a {
  width: 520px;
  height: 520px;
  top: -180px;
  right: -120px;
  background: radial-gradient(circle, var(--accent) 0%, transparent 68%);
  opacity: 0.16;
}
.bg-glow--b {
  width: 460px;
  height: 460px;
  bottom: -160px;
  left: -120px;
  background: radial-gradient(circle, var(--accent-2) 0%, transparent 66%);
  opacity: 0.13;
}
.bg-grid {
  position: absolute;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  background-image:
    linear-gradient(var(--glass-border) 1px, transparent 1px),
    linear-gradient(90deg, var(--glass-border) 1px, transparent 1px);
  background-size: 48px 48px;
  mask-image: radial-gradient(ellipse at 50% 30%, rgba(0, 0, 0, 0.5), transparent 70%);
  -webkit-mask-image: radial-gradient(ellipse at 50% 30%, rgba(0, 0, 0, 0.5), transparent 70%);
}

/* 标题栏 */
.titlebar {
  position: relative;
  z-index: 10;
  height: 44px;
  display: flex;
  align-items: center;
  padding-left: 16px;
  border-bottom: 1px solid var(--glass-border);
  background: var(--glass-bg);
  backdrop-filter: blur(18px) saturate(140%);
  -webkit-backdrop-filter: blur(18px) saturate(140%);
  user-select: none;
  flex-shrink: 0;
  /* Wails frameless 拖拽：整个标题栏可拖动窗口 */
  --wails-draggable: drag;
}
.titlebar__brand {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 700;
  font-size: 14px;
  letter-spacing: 0.02em;
}
.titlebar__logo {
  width: 22px;
  height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 7px;
  background: var(--accent-grad);
  color: #fff;
  font-size: 12px;
  box-shadow: 0 2px 10px rgba(56, 189, 248, 0.4);
}
.titlebar__tag {
  font-size: 11px;
  font-weight: 500;
  color: var(--text-3);
  letter-spacing: 0.06em;
  margin-left: 4px;
}
.titlebar__drag {
  flex: 1;
  height: 100%;
}
.titlebar__controls {
  display: flex;
  align-items: center;
  gap: 2px;
  padding-right: 8px;
  /* 按钮区不可拖拽，否则最小化/关闭按钮点不到 */
  --wails-draggable: no-drag;
}
.titlebar__btn {
  width: 40px;
  height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 7px;
  font-size: 13px;
  color: var(--text-2);
  transition: all var(--dur) var(--ease);
}
.titlebar__btn:hover {
  background: var(--hover);
  color: var(--text-1);
}
.titlebar__btn--close:hover {
  background: #e11d48;
  color: #fff;
}

/* 失焦自动锁定倒计时提示条 */
.autolock-bar {
  position: relative;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 6px 12px;
  font-size: 12.5px;
  color: var(--warning);
  background: var(--glass-bg);
  border-bottom: 1px solid var(--glass-border);
}

/* 主区域 */
.stage {
  position: relative;
  z-index: 1;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.stage-boot {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  color: var(--text-3);
  font-size: 13.5px;
}

/* 视图切换动画 */
.view-enter-active,
.view-leave-active {
  transition: opacity 0.22s var(--ease), transform 0.22s var(--ease);
}
.view-enter-from {
  opacity: 0;
  transform: translateY(10px);
}
.view-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}
</style>
