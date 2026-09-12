<script setup>
// 自绘下拉组件（替代原生 select：样式可控、与设计系统统一）。
// v-model 绑定选中值；options: [{value, label, icon?}]；disabled/hint 可选。
import {ref, computed, watch, onMounted, onBeforeUnmount} from 'vue'

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
const cur = computed(() => props.options.find((o) => o.value === props.modelValue))

function toggle() {
  if (props.disabled) return
  open.value = !open.value
}

function pick(o) {
  open.value = false
  if (o.value !== props.modelValue) {
    emit('update:modelValue', o.value)
    emit('change', o.value)
  }
}

// 点击组件外部关闭（document 级监听）
const onDocClick = (e) => {
  if (rootEl.value && !rootEl.value.contains(e.target)) open.value = false
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
    <button type="button" class="pb-select__trigger" :disabled="disabled" @click="toggle">
      <span class="pb-select__label">
        <span v-if="cur?.icon">{{ cur.icon }}</span>
        <span class="pb-truncate">{{ cur?.label ?? placeholder }}</span>
      </span>
      <span class="pb-select__caret">▾</span>
    </button>
    <div v-if="open" class="pb-select__menu">
      <div
          v-for="o in options"
          :key="o.value"
          class="pb-select__opt"
          :class="{'pb-select__opt--on': o.value === modelValue}"
          @click="pick(o)"
      >
        <span v-if="o.icon">{{ o.icon }}</span>
        <span class="pb-truncate">{{ o.label }}</span>
        <span v-if="o.value === modelValue" class="pb-select__check">✓</span>
      </div>
      <div v-if="hint" class="pb-select__hint">{{ hint }}</div>
    </div>
  </div>
</template>
