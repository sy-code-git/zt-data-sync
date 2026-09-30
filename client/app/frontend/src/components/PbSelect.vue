<script setup>
// 自绘下拉组件（替代原生 select：样式可控、与设计系统统一）。
// v-model 绑定选中值；options: [{value, label, icon?}]；disabled/hint 可选。
// 键盘可达（无障碍）：触发器是唯一 Tab 停靠点，方向键移动高亮项、Enter/Space 选中、
// Esc 关闭；同时带 listbox/option 语义与 aria-expanded/aria-activedescendant。
import {ref, computed, watch, nextTick, onMounted, onBeforeUnmount} from 'vue'

const props = defineProps({
  modelValue: {type: [String, Number], default: ''},
  options: {type: Array, default: () => []},
  disabled: {type: Boolean, default: false},
  hint: {type: String, default: ''},
  placeholder: {type: String, default: '请选择'},
})
const emit = defineEmits(['update:modelValue', 'change'])

const open = ref(false)
const rootEl = ref(null)
const menuEl = ref(null)
const activeIdx = ref(-1)
const cur = computed(() => props.options.find((o) => o.value === props.modelValue))
const listId = `pb-select-${Math.random().toString(36).slice(2, 9)}`
const activeId = computed(() => (activeIdx.value >= 0 ? `${listId}-opt-${activeIdx.value}` : undefined))

function scrollActiveIntoView() {
  nextTick(() => {
    const el = menuEl.value?.children?.[activeIdx.value]
    el?.scrollIntoView?.({block: 'nearest'})
  })
}

function openMenu() {
  if (props.disabled || open.value) return
  open.value = true
  const i = props.options.findIndex((o) => o.value === props.modelValue)
  activeIdx.value = i >= 0 ? i : 0
  scrollActiveIntoView()
}

function closeMenu() {
  open.value = false
  activeIdx.value = -1
}

function toggle() {
  if (props.disabled) return
  if (open.value) closeMenu()
  else openMenu()
}

function pick(o) {
  if (!o) return
  closeMenu()
  if (o.value !== props.modelValue) {
    emit('update:modelValue', o.value)
    emit('change', o.value)
  }
}

// 键盘操作：与原生 select 的直觉一致
function onTriggerKeydown(e) {
  if (props.disabled) return
  const n = props.options.length
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      if (!open.value) return openMenu()
      if (n) { activeIdx.value = (activeIdx.value + 1) % n; scrollActiveIntoView() }
      break
    case 'ArrowUp':
      e.preventDefault()
      if (!open.value) return openMenu()
      if (n) { activeIdx.value = (activeIdx.value - 1 + n) % n; scrollActiveIntoView() }
      break
    case 'Home':
      if (open.value && n) { e.preventDefault(); activeIdx.value = 0; scrollActiveIntoView() }
      break
    case 'End':
      if (open.value && n) { e.preventDefault(); activeIdx.value = n - 1; scrollActiveIntoView() }
      break
    case 'Enter':
    case ' ':
      e.preventDefault()
      if (!open.value) return openMenu()
      pick(props.options[activeIdx.value])
      break
    case 'Escape':
      if (open.value) { e.preventDefault(); closeMenu() }
      break
    case 'Tab':
      closeMenu() // 离开即收起，避免"悬空"菜单
      break
  }
}

// 点击组件外部关闭（document 级监听）
const onDocClick = (e) => {
  if (rootEl.value && !rootEl.value.contains(e.target)) closeMenu()
}
onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

// 选项变化后当前值失效时回退到首项（防显示空）。
// 注意：仅当选项"值集合"真实变化时才回退——用值签名而非 deep 监听数组，
// 避免父级每次渲染重建 options 数组（computed 每次访问生成新引用）导致误回退用户已选值。
watch(
    () => props.options.map((o) => o.value).join('\u0000'),
    () => {
      if (!props.options.some((o) => o.value === props.modelValue)) {
        if (props.options.length) emit('update:modelValue', props.options[0].value)
      }
    }
)
</script>

<template>
  <div ref="rootEl" class="pb-select" :class="{'pb-select--open': open}">
    <button
        type="button"
        class="pb-select__trigger"
        :disabled="disabled"
        aria-haspopup="listbox"
        :aria-expanded="open"
        :aria-controls="open ? listId : undefined"
        :aria-activedescendant="open ? activeId : undefined"
        @click="toggle"
        @keydown="onTriggerKeydown"
    >
      <span class="pb-select__label">
        <span v-if="cur?.icon">{{ cur.icon }}</span>
        <span class="pb-truncate">{{ cur?.label ?? placeholder }}</span>
      </span>
      <span class="pb-select__caret" aria-hidden="true">▾</span>
    </button>
    <div v-if="open" :id="listId" ref="menuEl" class="pb-select__menu" role="listbox">
      <div
          v-for="(o, i) in options"
          :id="`${listId}-opt-${i}`"
          :key="o.value"
          class="pb-select__opt"
          :class="{'pb-select__opt--on': o.value === modelValue, 'pb-select__opt--active': i === activeIdx}"
          role="option"
          :aria-selected="o.value === modelValue"
          @click="pick(o)"
          @mouseenter="activeIdx = i"
      >
        <span v-if="o.icon">{{ o.icon }}</span>
        <span class="pb-truncate">{{ o.label }}</span>
        <span v-if="o.value === modelValue" class="pb-select__check">✓</span>
      </div>
      <div v-if="hint" class="pb-select__hint">{{ hint }}</div>
    </div>
  </div>
</template>
