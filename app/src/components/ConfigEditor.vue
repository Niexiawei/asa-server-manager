<template>
  <t-dialog
      :visible="visible"
      :header="title"
      @confirm="handleConfirm"
      @close="handleCancel"
      :confirm-loading="saving"
      mode="full-screen"
  >
    <div ref="editorContainer" style="width: 100%; height: 100%"></div>
  </t-dialog>
</template>

<script setup>
import {ref, watch, nextTick, onUnmounted} from 'vue'
import * as monaco from 'monaco-editor'

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  title: {
    type: String,
    required: true
  },
  content: {
    type: String,
    default: ''
  },
  language: {
    type: String,
    default: 'ini'
  },
  saving: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:visible', 'save', 'cancel'])

const editorContainer = ref(null)
let editor = null

// 监听 visible 属性
watch(() => props.visible, (newVal) => {
  if (newVal && editorContainer.value) {
    nextTick(() => {
      initEditor()
    })
  } else if (!newVal && editor) {
    disposeEditor()
  }
})

// 初始化编辑器
const initEditor = () => {
  if (!editor && editorContainer.value) {
    editor = monaco.editor.create(editorContainer.value, {
      value: props.content,
      language: props.language,
      theme: 'vs-dark',
      automaticLayout: true,
      minimap: {enabled: true},
      fontSize: 13,
      lineNumbers: 'on',
      scrollBeyondLastLine: false,
      wordWrap: 'off',
      wrappingIndent: 'none',
      horizontalScrollbarSize: 12,
      cursorBlinking: 'blink',
      cursorSmoothCaretAnimation: 'on'
    })
  } else if (editor) {
    editor.setValue(props.content)
  }
}

// 销毁编辑器
const disposeEditor = () => {
  if (editor) {
    editor.dispose()
    editor = null
  }
}

// 获取编辑器内容
const getEditorContent = () => {
  return editor ? editor.getValue() : props.content
}

// 保存处理
const beforeOk = async () => {
  return new Promise((resolve) => {
    const content = getEditorContent()
    emit('save', content)
    // 等待保存完成
    resolve(true)
  })
}

const handleSave = () => {
  emit('update:visible', false)
}

const handleCancel = () => {
  emit('update:visible', false)
  emit('cancel')
}

const handleConfirm = async () => {
  await beforeOk()
  handleSave()
}

onUnmounted(() => {
  disposeEditor()
})
</script>

<style scoped>
/* 编辑器容器样式在模态框中由 body-style 控制 */
</style>
