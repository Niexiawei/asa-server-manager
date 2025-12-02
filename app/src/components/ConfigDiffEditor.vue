<template>
  <div class="config-diff-container">
    <div ref="diffEditorContainer" class="diff-editor-container"></div>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import * as monaco from 'monaco-editor'

const props = defineProps({
  originalContent: {
    type: String,
    default: ''
  },
  modifiedContent: {
    type: String,
    default: ''
  },
  language: {
    type: String,
    default: 'ini'
  },
  originalLabel: {
    type: String,
    default: 'Server Config'
  },
  modifiedLabel: {
    type: String,
    default: 'Instance Config'
  }
})

const diffEditorContainer = ref(null)
let diffEditor = null

// 初始化 Diff Editor
const initDiffEditor = () => {
  if (!diffEditor && diffEditorContainer.value) {
    const originalModel = monaco.editor.createModel(
      props.originalContent,
      props.language
    )
    const modifiedModel = monaco.editor.createModel(
      props.modifiedContent,
      props.language
    )

    diffEditor = monaco.editor.createDiffEditor(diffEditorContainer.value, {
      theme: 'vs-light',
      automaticLayout: true,
      fontSize: 12,
      lineNumbers: 'on',
      scrollBeyondLastLine: false,
      wordWrap: 'on',
      readOnly: true,
      originalEditable: false,
      modifiedEditable: false,
      minimap: { enabled: false },
      renderSideBySide: true,
      renderWhitespace: 'all'
    })

    diffEditor.setModel({
      original: originalModel,
      modified: modifiedModel
    })
  }
}

// 销毁 Diff Editor
const disposeDiffEditor = () => {
  if (diffEditor) {
    diffEditor.dispose()
    diffEditor = null
  }
}

// 监听原始内容变化
watch(() => props.originalContent, (newVal) => {
  if (diffEditor) {
    const model = diffEditor.getModel()
    if (model && model.original) {
      model.original.setValue(newVal)
    }
  }
})

// 监听修改后的内容变化
watch(() => props.modifiedContent, (newVal) => {
  if (diffEditor) {
    const model = diffEditor.getModel()
    if (model && model.modified) {
      model.modified.setValue(newVal)
    }
  }
})

// 监听语言变化
watch(() => props.language, (newVal) => {
  if (diffEditor) {
    const model = diffEditor.getModel()
    if (model) {
      monaco.editor.setModelLanguage(model.original, newVal)
      monaco.editor.setModelLanguage(model.modified, newVal)
    }
  }
})

onMounted(() => {
  initDiffEditor()
})

onUnmounted(() => {
  disposeDiffEditor()
})
</script>

<style scoped>
.config-diff-container {
  width: 100%;
  height: 70vh;
  display: flex;
  flex-direction: column;
}

.diff-editor-container {
  width: 100%;
  height: 100%;
  border: 1px solid #d9d9d9;
  border-radius: 4px;
  overflow: hidden;
}
</style>
