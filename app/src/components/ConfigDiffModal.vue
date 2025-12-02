<template>
  <a-modal
      v-model:visible="modalVisible"
      :title="diffType === 'game-ini' ? 'Game.ini 对比' : 'GameUserSettings.ini 对比'"
      :width="1600"
      :height="900"
      :mask="true"
      unmountOnClose
      :footer="false"
      class="diff-modal"
  >
    <div class="diff-modal-wrapper">
      <div class="diff-modal-toolbar" v-if="!toolbarCollapsed">
        <div class="toolbar-container">
          <div class="toolbar-label">左侧：服务器基础配置</div>
          <div class="toolbar-label">右侧：实例配置</div>
        </div>
      </div>
      <a-spin :loading="loading" class="diff-editor-spinner">
        <config-diff-editor
            v-if="!loading"
            :original-content="diffType === 'game-ini' ? serverGameIniContent : serverGameUserSettingsContent"
            :modified-content="diffType === 'game-ini' ? gameIniContent : gameUserSettingsContent"
            language="ini"
            :original-label="'服务器配置'"
            :modified-label="'实例配置'"
        />
      </a-spin>
    </div>
  </a-modal>
</template>

<script setup>
import { ref, watch, computed } from 'vue'
import ConfigDiffEditor from './ConfigDiffEditor.vue'

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
  loading: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:visible'])

const modalVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const toolbarCollapsed = ref(false)
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
    flex:1;
  font-size: 16px;
  color: #333;
  font-weight: 600;
  letter-spacing: 0.5px;
}
</style>
