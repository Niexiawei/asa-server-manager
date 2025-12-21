<template>
  <div class="config-viewer-container">
    <div ref="editorContainer" class="editor-container"></div>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import * as monaco from 'monaco-editor'

const props = defineProps({
  content: {
    type: String,
    default: ''
  },
  language: {
    type: String,
    default: 'ini'
  }
})

const editorContainer = ref(null)
let editor = null

// 初始化编辑器
const initEditor = () => {
  if (!editor && editorContainer.value) {
    editor = monaco.editor.create(editorContainer.value, {
      value: props.content,
      language: props.language,
      theme: 'vs-light',
      automaticLayout: true,
      minimap: { enabled: true },
      fontSize: 12,
      lineNumbers: 'on',
      scrollBeyondLastLine: false,
      wordWrap: 'off',
      wrappingIndent: 'none',
      horizontalScrollbarSize: 12,
      readOnly: true, // 只读模式
      contextMenu: false, // 禁用右键菜单
      domReadOnly: true // DOM 只读
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

// 监听 content 属性变化
watch(() => props.content, (newVal) => {
  if (editor) {
    editor.setValue(newVal)
  }
})

onMounted(() => {
  initEditor()
})

onUnmounted(() => {
  disposeEditor()
})
</script>

<style scoped>
.config-viewer-container {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
}

.editor-container {
  width: 100%;
  height: 100%;
  border: 1px solid #d9d9d9;
  border-radius: 4px;
  overflow: hidden;
}
</style>
