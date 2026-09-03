<template>
  <div class="config-files-tab">
    <t-alert class="running-hint" v-if="running" theme="info" message="服务器运行中，配置文件为只读，停止后可编辑。"/>
    <div class="files-grid">
      <t-card title="Game.ini 配置" class="file-card" headerBordered>
        <template #actions>
          <t-button
              theme="primary"
              :loading="savingGameIni"
              :disabled="running"
              @click="onSaveGameIni"
          >
            <template #icon>
              <SaveIcon/>
            </template>
            保存
          </t-button>
        </template>
        <div class="editor-wrap">
          <ini-editor-pane
              ref="gameIniPaneRef"
              :model-value="gameIniContent"
              :readonly="running"
              @change="(v) => onChange('gameIni', v)"
          />
        </div>
      </t-card>

      <t-card title="GameUserSettings.ini 配置" class="file-card" headerBordered>
        <template #actions>
          <t-button
              theme="primary"
              :loading="savingGus"
              :disabled="running"
              @click="onSaveGus"
          >
            <template #icon>
              <SaveIcon/>
            </template>
            保存
          </t-button>
        </template>
        <div class="editor-wrap">
          <ini-editor-pane
              ref="gusPaneRef"
              :model-value="gameUserSettingsContent"
              :readonly="running"
              @change="(v) => onChange('gus', v)"
          />
        </div>
      </t-card>
    </div>
  </div>
</template>

<script setup>
import {reactive, ref, watch} from 'vue'
import IniEditorPane from './IniEditorPane.vue'
import {SaveIcon} from "tdesign-icons-vue-next";

const props = defineProps({
  gameIniContent: {type: String, default: ''},
  gameUserSettingsContent: {type: String, default: ''},
  running: {type: Boolean, default: false},
  savingGameIni: {type: Boolean, default: false},
  savingGus: {type: Boolean, default: false},
})
const emit = defineEmits(['save-game-ini', 'save-gus', 'update:dirty'])

const gameIniPaneRef = ref(null)
const gusPaneRef = ref(null)

// 当前编辑器里的值；与 props（最后一次加载/保存的内容）比对得脏状态
const cur = reactive({
  gameIni: props.gameIniContent,
  gus: props.gameUserSettingsContent,
})

const recomputeDirty = () => {
  const dirty =
      cur.gameIni !== props.gameIniContent || cur.gus !== props.gameUserSettingsContent
  emit('update:dirty', dirty)
}

const onChange = (key, v) => {
  cur[key] = v
  recomputeDirty()
}

watch(
    () => props.gameIniContent,
    (v) => {
      cur.gameIni = v
      recomputeDirty()
    },
)
watch(
    () => props.gameUserSettingsContent,
    (v) => {
      cur.gus = v
      recomputeDirty()
    },
)

const onSaveGameIni = () => emit('save-game-ini', gameIniPaneRef.value?.getValue() ?? cur.gameIni)
const onSaveGus = () => emit('save-gus', gusPaneRef.value?.getValue() ?? cur.gus)
</script>

<style scoped lang="less">
.config-files-tab {
  display: flex;
  flex-direction: column;
  gap: 8px;
  height: 100%;
  padding: 12px 0;
  box-sizing: border-box;

  :deep(.t-card) {
    .t-card__header {
      flex: 0 0 auto;
    }

    > .t-loading__parent {
      flex: 1 1 auto;
      min-height: 0;
    }
  }
}

.files-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  width: 100%;
  flex: 1 1 auto;
  min-height: 0;
}

@media (max-width: 1100px) {
  .files-grid {
    grid-template-columns: 1fr;
  }
}

.file-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  box-sizing: border-box;

  :deep(.t-card__body) {
    width: 100%;
    box-sizing: border-box;
    height: 100%;
  }
}

.editor-wrap {
  width: 100%;
  height: 100%;
  min-height: 0;
  box-sizing: border-box;
  overflow: hidden;
}

.running-hint {
  font-size: 16px;
  flex: 0 0 auto;
}
</style>
