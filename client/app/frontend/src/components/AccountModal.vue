<script setup>
// AccountModal.vue —— 账号详情弹窗（**唯一实现**，ListView 与 WorkbenchView 共用）。
//
// 交互纪律（§9.2）：密码字段默认掩码，点 👁 显示后 20 秒自动重新掩码；
// 任何复制都在 30 秒后自动清空剪贴板。弹窗带焦点陷阱与 Esc 关闭。
//
// 为什么要抽成组件：此前 ListView 与 WorkbenchView 各写一份（约 170 行），连
// 「先关弹窗再读 accModal.entry」这个 TypeError 都是两边各踩一次、各修一次。
// 新增任何"账号详情"能力（如 OTP、附件）只应改这里一处。
import {computed, onBeforeUnmount, onMounted, ref} from 'vue'
import {useModalFocus} from '../composables/useModalFocus'
import {useAppStore} from '../store'

const store = useAppStore()

const props = defineProps({
  entry: {type: Object, required: true},
  // 归属行文案（由父级拼好：环境 · IP · 备注），父级各自有不同来源
  subtitle: {type: String, default: ''},
})
const emit = defineEmits(['close', 'edit', 'delete'])

const modalRef = ref(null)
useModalFocus(modalRef, computed(() => true)) // 组件按需挂载，激活态恒为真

const PW_RE = /pass|secret|token|pwd|密码/i
const REVEAL_MS = 20000
const CLEAR_MS = 30000

const revealed = ref(false)
let revealTimer = null
let clearTimer = null

// 非密码字段（明文展示 + 单字段复制）
const plainFields = computed(() =>
    Object.entries(props.entry.fields || {}).filter(([k]) => !PW_RE.test(k)))
// 密码字段（掩码展示 + 显示切换 + 复制）
const pwField = computed(() =>
    Object.entries(props.entry.fields || {}).find(([k]) => PW_RE.test(k)) || null)
const title = computed(() => props.entry.fields?.username || props.entry.title)

function toggleReveal() {
  revealed.value = !revealed.value
  clearTimeout(revealTimer)
  revealTimer = revealed.value ? setTimeout(() => { revealed.value = false }, REVEAL_MS) : null
}

function copyText(text) {
  if (!text) return
  navigator.clipboard?.writeText(String(text))
  store.toast('已复制到剪贴板，30 秒后自动清空', 'success')
  clearTimeout(clearTimer)
  clearTimer = setTimeout(() => navigator.clipboard?.writeText(''), CLEAR_MS)
}

// 编辑/删除入口：把条目交给父级处理（父级负责关弹窗 + 跳转），
// 避免"先关弹窗再读 entry"的顺序陷阱
function onEdit() {
  emit('edit', props.entry)
}

function onDelete() {
  emit('delete', props.entry)
}

const onKeydown = (e) => {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => document.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown)
  clearTimeout(revealTimer)
  clearTimeout(clearTimer)
})
</script>

<template>
  <div class="pb-modal-mask" @click.self="emit('close')">
    <div ref="modalRef" class="pb-modal pb-glass pb-glass--strong" role="dialog" aria-modal="true"
         :aria-label="`账号 ${title}`">
      <div class="pb-modal__head">
        <span class="pb-modal__title">账号 · {{ title }}</span>
        <button class="pb-iconbtn" title="关闭" aria-label="关闭" @click="emit('close')">✕</button>
      </div>
      <div class="pb-modal__body">
        <div v-if="subtitle" class="pb-xs pb-muted">{{ subtitle }}</div>
        <div v-for="([k, v]) in plainFields" :key="k" class="detail-field">
          <span class="detail-field__label">{{ k }}</span>
          <div class="detail-field__value">
            <span class="pb-truncate pb-fill pb-mono">{{ v }}</span>
            <button class="pb-iconbtn" :title="`复制 ${k}`" :aria-label="`复制 ${k}`" @click="copyText(v)">⧉</button>
          </div>
        </div>
        <template v-if="pwField">
          <div class="detail-field">
            <span class="detail-field__label">{{ pwField[0] }}</span>
            <div class="detail-field__value">
              <span class="pb-truncate pb-fill pb-mono">{{ revealed ? pwField[1] : '••••••••' }}</span>
              <button
                  class="detail-field__reveal"
                  :title="revealed ? '隐藏密码' : `显示密码（${REVEAL_MS / 1000} 秒后自动掩码）`"
                  :aria-label="revealed ? '隐藏密码' : '显示密码'"
                  :aria-pressed="revealed"
                  @click="toggleReveal"
              >👁</button>
              <button class="pb-iconbtn" title="复制密码" aria-label="复制密码" @click="copyText(pwField[1])">⧉</button>
            </div>
          </div>
          <p class="pb-xs pb-muted" style="font-size: 12px">
            密码显示 {{ REVEAL_MS / 1000 }} 秒后自动重新掩码 · 复制后 {{ CLEAR_MS / 1000 }} 秒自动清空剪贴板
          </p>
        </template>
      </div>
      <div class="pb-modal__foot">
        <button class="pb-btn pb-btn--danger" @click="onDelete">🗑 删除此账号</button>
        <button class="pb-btn pb-btn--ghost" @click="onEdit">✏ 编辑此账号</button>
        <button
            class="pb-btn pb-btn--primary"
            @click="copyText(`${title}\n${pwField?.[1] || ''}`)"
        >⧉ 复制账号+密码</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 字段行（原 ListView 的 .detail-field 与 WorkbenchView 的 .wb-detail-field 合并为一份） */
.detail-field {
  display: grid;
  grid-template-columns: 96px 1fr;
  gap: 10px;
  align-items: center;
  padding: 10px 12px;
  border-radius: var(--radius-md);
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
}
.detail-field__label {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-2);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.detail-field__value {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
/* 揭示按钮：沿用原全局 .detail-field__reveal 的 24×24 规格（此处为唯一定义处） */
.detail-field__reveal {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  color: var(--text-3);
  font-size: 13px;
  flex-shrink: 0;
  cursor: pointer;
  transition: all var(--dur) var(--ease);
}
.detail-field__reveal:hover {
  background: var(--active);
  color: var(--text-1);
}
</style>
