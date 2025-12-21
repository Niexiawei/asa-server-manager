<template>
  <a-modal
      v-model:visible="modalVisible"
      :title="diffType === 'game-ini' ? 'Game.ini 对比' : 'GameUserSettings.ini 对比'"
      :width="1600"
      :height="900"
      :mask="true"
      unmountOnClose
      :footer="editable"
      class="diff-modal"
  >
    <div class="diff-modal-wrapper">
      <div class="diff-modal-toolbar" v-if="!toolbarCollapsed">
        <div class="toolbar-container">
          <div class="toolbar-label">左侧：服务器基础配置</div>
          <div class="toolbar-label">右侧：实例配置</div>
        </div>
      </div>
      <a-spin :loading="dataLoading" class="diff-editor-spinner">
        <div class="editor-wrapper">
          <div ref="diffEditorContainer" class="diff-editor-container"></div>
        </div>
      </a-spin>
    </div>
    <template #footer>
      <div class="editor-toolbar">
        <a-button type="primary" @click="saveModifiedContent" :loading="savingLoading">保存修改</a-button>
        <a-button @click="resetModifiedContent" style="margin-left: 8px">放弃修改</a-button>
      </div>
    </template>
  </a-modal>
</template>

<script setup>
import {ref, watch, computed, onMounted, onUnmounted} from 'vue'
import * as monaco from 'monaco-editor'

const dataLoading = defineModel("dataLoading")
const savingLoading = defineModel("savingLoading", {
  default: false
})

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  diffType: {
    type: String,
    default: 'game-ini' // 'game-ini' 或 'game-user-settings'
  },
  gameIniContent: {
    type: String,
    default: ''
  },
  gameUserSettingsContent: {
    type: String,
    default: ''
  },
  serverGameIniContent: {
    type: String,
    default: ''
  },
  serverGameUserSettingsContent: {
    type: String,
    default: ''
  },
  editable: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:visible', 'save'])

const modalVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const toolbarCollapsed = ref(false)
const diffEditorContainer = ref(null)
let diffEditor = null

// 初始化 Diff Editor
const initDiffEditor = () => {
  if (!diffEditor && diffEditorContainer.value) {
    const originalContent = props.diffType === 'game-ini' ? props.serverGameIniContent : props.serverGameUserSettingsContent
    const modifiedContent = props.diffType === 'game-ini' ? props.gameIniContent : props.gameUserSettingsContent

    const originalModel = monaco.editor.createModel(
        originalContent,
        'ini'
    )
    const modifiedModel = monaco.editor.createModel(
        modifiedContent,
        'ini'
    )

    diffEditor = monaco.editor.createDiffEditor(diffEditorContainer.value, {
      theme: 'vs-light',
      automaticLayout: true,
      fontSize: 12,
      lineNumbers: 'on',
      scrollBeyondLastLine: false,
      wordWrap: 'off',
      wrappingIndent: 'none',
      horizontalScrollbarSize: 12,
      readOnly: false,
      originalEditable: false,
      minimap: {enabled: true},
      renderSideBySide: true,
      renderWhitespace: 'all'
    })

    const minimapOpts = {
      minimap: {
        enabled: true,          // 是否显示 minimap
        renderCharacters: true,// 仅显示块状缩略（节省宽度）
        maxColumn: 120          // 用于计算缩略比例
      },
      // 其他有用选项
      automaticLayout: true,
      scrollBeyondLastLine: false
    }

    let modifiedEdit = diffEditor.getModifiedEditor()
    let originalEdit = diffEditor.getOriginalEditor()
    modifiedEdit.updateOptions({
      readOnly: !props.editable,
      ...minimapOpts
    })
    originalEdit.updateOptions({
      ...minimapOpts
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
watch(() => ({
  type: props.diffType,
  serverGameIni: props.serverGameIniContent,
  serverGameUserSettings: props.serverGameUserSettingsContent
}), () => {
  if (diffEditor) {
    const model = diffEditor.getModel()
    if (model && model.original) {
      const newOriginalContent = props.diffType === 'game-ini' ? props.serverGameIniContent : props.serverGameUserSettingsContent
      model.original.setValue(newOriginalContent)
    }
  }
})

// 监听修改后的内容变化
watch(() => ({
  type: props.diffType,
  gameIni: props.gameIniContent,
  gameUserSettings: props.gameUserSettingsContent
}), () => {
  if (diffEditor) {
    const model = diffEditor.getModel()
    if (model && model.modified) {
      const newModifiedContent = props.diffType === 'game-ini' ? props.gameIniContent : props.gameUserSettingsContent
      model.modified.setValue(newModifiedContent)
    }
  }
})

// 监听可编辑状态变化
watch(() => props.editable, (newVal) => {
  if (diffEditor) {
    diffEditor.updateOptions({
      modifiedEditable: newVal
    })
  }
})

// 保存修改的内容
const saveModifiedContent = async () => {
  if (diffEditor) {
    const model = diffEditor.getModel()
    if (model && model.modified) {
      const newContent = model.modified.getValue()
      emit('save', {
        type: props.diffType,
        content: newContent,
      })
    }
  }
}

// 复位修改
const resetModifiedContent = () => {
  modalVisible.value = false
}

// 监听 visible 变化，控制编辑器初始化
watch(() => props.visible, (newVal) => {
  if (newVal && !diffEditor) {
    // 使用 nextTick 确保 DOM 已更新
    setTimeout(() => {
      initDiffEditor()
    }, 100)
  } else if (!newVal) {
    disposeDiffEditor()
  }
})

onMounted(() => {
  if (props.visible) {
    setTimeout(() => {
      initDiffEditor()
    }, 100)
  }
})

onUnmounted(() => {
  disposeDiffEditor()
})
</script>

<style scoped>
/* Diff Modal 调整 */
.diff-modal {
  :deep(.arco-modal-body) {
    padding: 0;
    height: calc(100vh - 150px);
    overflow: hidden;
  }

  :deep(.arco-modal-header) {
    padding: 20px;
    border-bottom: 1px solid #dfe1e6;
  }
}

.diff-modal-wrapper {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.diff-editor-spinner {
  flex: 1;
  display: flex;
  width: 100%;
  height: 100%;
  overflow: hidden;

  :deep(.arco-spin-content) {
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
  }
}

.editor-wrapper {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.editor-toolbar {

}

.diff-editor-container {
  width: 100%;
  height: 65vh;
  border: 1px solid #d9d9d9;
  border-radius: 4px;
  overflow: hidden;
}

.diff-modal-toolbar {
  flex-shrink: 0;
  padding: 16px 20px;
  background-color: #f5f5f5;
  display: flex;
  align-items: center;
  margin-bottom: 15px;
  border-radius: 8px;
}

.toolbar-container {
  display: flex;
  align-items: center;
  width: 100%;
  justify-content: center;
}

.toolbar-label {
  flex: 1;
  font-size: 16px;
  color: #333;
  font-weight: 600;
  letter-spacing: 0.5px;
}
</style>
