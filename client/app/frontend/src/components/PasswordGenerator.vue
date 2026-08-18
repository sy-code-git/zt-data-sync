<script setup>
import {ref, watch, computed, onMounted, onBeforeUnmount} from 'vue'
import {api} from '../api'
import {useModalFocus} from '../composables/useModalFocus'

const emit = defineEmits(['generate', 'close'])

const length = ref(16)
const upper = ref(true)
const lower = ref(true)
const digits = ref(true)
const symbols = ref(false)
const excludeAmbiguous = ref(true)
const current = ref('')
const failed = ref(false)
const busy = ref(false)

// 弹窗焦点陷阱（组件挂载即弹窗打开）
const modalRef = ref(null)
useModalFocus(modalRef, computed(() => true))

async function generate() {
  busy.value = true
  failed.value = false
  try {
    current.value = await api.GeneratePassword(
        length.value, upper.value, lower.value, digits.value,
        symbols.value, excludeAmbiguous.value)
  } catch (e) {
    current.value = ''
    failed.value = true
  } finally {
    busy.value = false
  }
}

function usePw() {
  emit('generate', current.value)
}

watch([upper, lower, digits, symbols], () => {
  if (!upper.value && !lower.value && !digits.value && !symbols.value) {
    lower.value = true
  }
})

onMounted(() => {
  generate()
  // Esc 关闭弹窗（无障碍）
  document.addEventListener('keydown', onKeydown)
})
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))
function onKeydown(e) {
  if (e.key === 'Escape') emit('close')
}
</script>

<template>
  <div class="pb-modal-mask" @click.self="emit('close')">
    <div ref="modalRef" class="pb-modal pb-glass pb-glass--strong" role="dialog" aria-modal="true" aria-label="密码生成器">
      <div class="pb-modal__head">
        <span class="pb-modal__title">⚡ 密码生成器</span>
        <button class="pb-iconbtn" @click="emit('close')">✕</button>
      </div>

      <div class="pb-modal__body">
        <div class="gen-box">
          <span class="pb-mono gen-value" :class="{ 'gen-value--err': failed }">{{ failed ? '生成失败，请重试' : current }}</span>
          <button class="pb-btn pb-btn--ghost pb-btn--sm" :disabled="busy" @click="generate">⟳ 重新生成</button>
        </div>

        <div class="gen-row">
          <label class="pb-label" style="min-width: 60px">长度</label>
          <input v-model.number="length" type="range" min="4" max="64" style="flex:1"/>
          <span class="pb-mono gen-num">{{ length }}</span>
        </div>

        <div class="gen-row">
          <label class="pb-label" style="min-width: 60px">字符集</label>
          <div class="gen-check">
            <label><input v-model="upper" type="checkbox"/> A-Z</label>
            <label><input v-model="lower" type="checkbox"/> a-z</label>
            <label><input v-model="digits" type="checkbox"/> 0-9</label>
            <label><input v-model="symbols" type="checkbox"/> !@#$</label>
          </div>
        </div>

        <label class="gen-row" style="cursor: pointer">
          <input v-model="excludeAmbiguous" type="checkbox"/>
          <span>排除易混淆字符（0/O/1/l/I）</span>
        </label>
      </div>

      <div class="pb-modal__foot">
        <button class="pb-btn pb-btn--ghost" @click="emit('close')">取消</button>
        <button class="pb-btn pb-btn--primary" :disabled="!current" @click="usePw">填入条目</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.gen-box {
  display: flex;
  align-items: center;
  gap: 10px;
}
.gen-value {
  flex: 1;
  padding: 12px 14px;
  border-radius: var(--radius-md);
  background: var(--input-bg);
  border: 1px solid var(--glass-border);
  font-size: 14px;
  letter-spacing: 0.05em;
  word-break: break-all;
  min-height: 46px;
}
.gen-value--err {
  color: var(--danger);
  font-size: 13px;
  letter-spacing: 0;
}
.gen-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.gen-num {
  min-width: 28px;
  text-align: right;
  color: var(--accent);
  font-weight: 700;
}
.gen-check {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  font-size: 13px;
}
.gen-check label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
}
input[type='range'] {
  accent-color: var(--accent);
}
</style>
