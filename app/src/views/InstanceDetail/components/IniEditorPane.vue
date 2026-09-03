<template>
  <div ref="containerRef" class="ini-editor-pane"></div>
</template>

<script setup>
import {onBeforeUnmount, onMounted, ref, watch} from 'vue'
import * as monaco from 'monaco-editor'

// 可编辑的 Monaco 单文件编辑器（参照 ConfigEditor.vue 的初始化，但不套弹窗）。
// readonly 为真时整个编辑器只读；外部 modelValue 变化（加载 / 保存后刷新）会同步进来且不触发 change。
const props = defineProps({
  modelValue: {type: String, default: ''},
  language: {type: String, default: 'ini'},
  readonly: {type: Boolean, default: false},
})
const emit = defineEmits(['change'])

const containerRef = ref(null)
let editor = null
let applyingExternal = false

onMounted(() => {
  editor = monaco.editor.create(containerRef.value, {
    value: props.modelValue,
    language: props.language,
    theme: 'vs-dark',
    automaticLayout: true,
    minimap: {enabled: true},
    fontSize: 13,
    lineNumbers: 'on',
    scrollBeyondLastLine: false,
    wordWrap: 'off',
    wrappingIndent: 'none',
    readOnly: props.readonly,
  })
  editor.onDidChangeModelContent(() => {
    if (applyingExternal) return
    emit('change', editor.getValue())
  })
})

watch(
    () => props.modelValue,
    (val) => {
      if (editor && val !== editor.getValue()) {
        applyingExternal = true
        editor.setValue(val ?? '')
        applyingExternal = false
      }
    },
)

watch(
    () => props.readonly,
    (ro) => editor?.updateOptions({readOnly: ro}),
)

const getValue = () => (editor ? editor.getValue() : props.modelValue)

defineExpose({getValue})

onBeforeUnmount(() => {
  editor?.dispose()
  editor = null
})
</script>

<style scoped>
.ini-editor-pane {
  width: 100%;
  height: 100%;
}
</style>
