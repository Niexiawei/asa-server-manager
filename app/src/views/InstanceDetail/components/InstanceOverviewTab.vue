<template>
  <div class="overview-tab">
    <div class="config-resource-row">
      <t-card headerBordered class="config-section server-config">
        <template #title>
          <div class="config-card-title"><span>服务器配置</span></div>
        </template>
        <div class="config-grid">
          <div
              v-for="item in configItems"
              :key="item.label"
              class="config-grid-item"
              :class="{ 'full-width': item.label === '自定义启动参数', 'modid-item': item.label === 'Mod' }"
          >
            <div class="config-item">
              <div class="config-item-label">{{ item.label }}</div>
              <div class="config-item-content">
                <div
                    v-if="(!item.type || item.type === 'text') && item.label === 'Mod'"
                    class="config-item-value"
                >
                  <div v-if="item.value && item.value !== '-'">
                    <div class="mod-container">
                      <t-tag
                          v-for="modId in item.value.split(',')"
                          :key="modId"
                          class="mod-tag"
                          theme="primary"
                          @click="copyModId(modId.trim())"
                      >
                        {{ getModNameById(modId.trim()) || modId.trim() }}
                        <file-copy-icon :style="{ fontSize: '12px' }"/>
                      </t-tag>
                      <div class="break"></div>
                      <t-tag class="copy-all-btn" @click="copyAllModIds(item.value)">
                        <file-copy-icon/>
                        复制全部
                      </t-tag>
                    </div>
                  </div>
                  <div v-else>{{ item.value }}</div>
                </div>
                <div
                    v-if="(!item.type || item.type === 'text') && item.label !== 'Mod'"
                    class="config-item-value"
                >
                  {{ item.value }}
                </div>
                <div v-if="item.type === 'boolean'" class="config-item-value">
                  <t-tag :theme="item.value === '是' ? 'success' : 'default'">{{ item.value }}</t-tag>
                </div>
                <div v-if="item.type === 'password'" class="password-wrapper">
                  <span class="config-item-value">
                    {{
                      item.label === '服务器密码' && showServerPassword
                          ? item.value
                          : item.label === '管理员密码' && showAdminPassword
                              ? item.value
                              : item.hasPassword
                                  ? '●●●●●●'
                                  : item.value
                    }}
                  </span>
                  <t-button
                      v-if="item.hasPassword"
                      variant="text"
                      size="small"
                      @click="
                      item.label === '服务器密码'
                        ? (showServerPassword = !showServerPassword)
                        : (showAdminPassword = !showAdminPassword)
                    "
                  >
                    <template #icon>
                      <component
                          :is="
                          item.label === '服务器密码'
                            ? showServerPassword
                              ? BrowseOffIcon
                              : BrowseIcon
                            : showAdminPassword
                              ? BrowseOffIcon
                              : BrowseIcon
                        "
                      />
                    </template>
                  </t-button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </t-card>
      <div class="info-right" v-if="baseLoaded">
        <t-card class="config-section status-history-card" headerBordered>
          <template #title>
            <div class="config-card-title"><span>实例历史状态</span></div>
          </template>
          <instance-status-history :instance-name="instanceName"/>
        </t-card>
      </div>
      <t-card class="info-right" v-else>
        <t-skeleton :animation="true" theme="paragraph"></t-skeleton>
        <t-skeleton :animation="true" theme="paragraph"></t-skeleton>
        <t-skeleton :animation="true" theme="paragraph"></t-skeleton>
        <t-skeleton :animation="true" theme="paragraph"></t-skeleton>
      </t-card>
    </div>
    <div class="config-resource-row config-resource-row-bottom">
      <t-card class="config-section resource-monitor-card" headerBordered>
        <template #title>
          <div class="config-card-title"><span>资源占用</span></div>
        </template>
        <resource-monitor :show-title-div="false" :instance-name="instanceName"/>
      </t-card>
      <t-card class="config-section resource-trend-card" headerBordered>
        <template #title>
          <div class="config-card-title"><span>资源趋势</span></div>
        </template>
        <resource-trend-panel scope="instance" :instance-name="instanceName"/>
      </t-card>
    </div>
  </div>
</template>

<script setup>
import {computed, ref} from 'vue'
import {FileCopyIcon, BrowseIcon, BrowseOffIcon} from 'tdesign-icons-vue-next'
import {MessagePlugin} from 'tdesign-vue-next'
import {useClipboard} from '@vueuse/core'
import ResourceMonitor from '@/components/ResourceMonitor.vue'
import ResourceTrendPanel from '@/components/ResourceTrendPanel.vue'
import InstanceStatusHistory from '@/components/InstanceStatusHistory.vue'

const props = defineProps({
  config: {type: Object, default: () => ({})},
  asaVersion: {type: [String, Number], default: ''},
  instanceName: {type: String, required: true},
  modInfo: {type: Array, default: () => []},
  baseLoaded: {type: Boolean, default: false},
})

const showServerPassword = ref(false)
const showAdminPassword = ref(false)

const configItems = computed(() => {
  const config = props.config || {}
  return [
    {label: '实例名称', value: props.instanceName},
    {label: '服务器名称', value: config.ServerName || '-'},
    {label: '最大玩家数', value: config.MaxPlayers || '-'},
    {label: '游戏端口', value: config.Port || '-'},
    {label: 'RCON端口', value: config.RCONPort || '-'},
    {label: '绑定域名', value: config.BindDomain || '-'},
    {label: '地图名称', value: config.MapName || '-'},
    {label: 'Mod', value: config.ModIDs || '-'},
    {label: '存档目录', value: config.SaveDir || '-'},
    {label: '启用ASA插件', value: config.EnableAsaPlugin ? '是' : '否', type: 'boolean'},
    {label: '服务器版本', value: 'v' + props.asaVersion},
    {label: '集群ID', value: config.ClusterID || '-', type: 'text'},
    {label: 'Message Of The Day', value: config.MessageOfTheDay || '-', type: 'text'},
    {label: '消息时长', value: config.MessageOfTheDayDuration || '-', type: 'text'},
    {
      label: '服务器密码',
      value: config.ServerPassword || '-',
      type: 'password',
      hasPassword: !!config.ServerPassword,
    },
    {
      label: '管理员密码',
      value: config.ServerAdminPassword || '-',
      type: 'password',
      hasPassword: !!config.ServerAdminPassword,
    },
    {label: '自定义启动参数', value: config.CustomStartParameters || '-', type: 'text'},
  ]
})

const getModNameById = (modId) => {
  if (!modId) return null
  const mod = props.modInfo.find((m) => m.id === modId)
  return mod ? mod.name : null
}

const {copy} = useClipboard({legacy: true})

const copyModId = async (modId) => {
  try {
    await copy(modId)
    MessagePlugin.success(`${getModNameById(modId) || modId}:已复制到剪切板`)
  } catch (error) {
    MessagePlugin.error('复制失败:' + error)
  }
}

const copyAllModIds = async (modIds) => {
  try {
    const ids = modIds
        .split(',')
        .map((id) => id.trim())
        .filter((id) => id)
        .join(',')
    await copy(ids)
    MessagePlugin.success('已复制所有Mod ID到剪切板')
  } catch (error) {
    MessagePlugin.error('复制失败:' + error)
  }
}
</script>

<style scoped lang="less">
.overview-tab {
  display: flex;
  flex-direction: column;
  gap: 15px;
}

.config-resource-row-bottom{
  grid-template-columns: 1fr 3fr !important;
}

.config-resource-row {
  display: grid;
  grid-template-columns: 3fr 1fr;
  gap: 15px;
  width: 100%;

  .resource-monitor-card {
    flex: 0 0 auto;
  }

  .resource-trend-card {
    flex: 0 0 auto;
  }

  .status-history-card {
    flex: 1 1 0;
    min-height: 0;
  }

  .info-right {
    display: flex;
    flex-direction: column;
    height: 100%;
    gap: 15px;

    :deep(.t-skeleton) {
      padding-bottom: 12px;

      &:last-child {
        padding-bottom: 0;
      }
    }
  }
}

@media (max-width: 1100px) {
  .config-resource-row {
    grid-template-columns: 1fr;
  }
}

.resource-monitor-card {
  :deep(.t-card__body) {
    box-sizing: border-box;
    padding: 15px !important;
  }
}

.resource-trend-card {
  :deep(.t-card__body) {
    box-sizing: border-box;
    padding: 15px !important;
  }
}

.config-section {
  border-radius: 6px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08);

  :deep(.t-loading__parent) {
    height: calc(100% - 56px);
  }

  :deep(.t-card__body) {
    height: 100%;
    box-sizing: border-box;
  }
}

.config-card-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.config-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 32px;
  background-color: #f5f5f5;
  border-radius: 4px;
  padding: 0 16px;
}

.config-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.config-grid-item {
  display: flex;
  flex-direction: column;
}

.config-grid-item.modid-item {
  width: 100%;
  grid-column: 1 / -1;

  .config-item {
    width: calc(100% - 32px);
    height: auto !important;
    min-height: 32px;
    align-items: flex-start !important;
    flex-direction: column;
  }

  .config-item-label {
    flex: 0 0 auto;
    white-space: nowrap;
    padding: 12px 0 !important;
    box-sizing: border-box;
    height: auto !important;
    line-height: 20px;
  }

  .config-item-content {
    flex: 1 1 0;
    min-width: 0;
    height: auto !important;
    padding: 0 0 !important;
    box-sizing: border-box;

    .config-item-value {
      width: 100%;
      display: flex;
      flex-direction: column;
      gap: 8px;

      .mod-container {
        display: flex;
        flex-wrap: wrap;
        gap: 4px;

        > .t-tag {
          cursor: pointer;
        }

        .break {
          flex: 0 0 100%;
          height: 0;
        }
      }

      .copy-all-btn {
        align-self: flex-start;
        font-size: 12px;
        padding: 4px 8px;
        height: auto;
      }
    }
  }
}

.config-grid-item.full-width {
  grid-column: 1 / -1;
}

.config-grid-item .config-item {
  height: 26px;
  padding: 12px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.config-item-label {
  font-weight: 600;
  color: #333;
  min-width: 120px;
  font-size: 14px;
}

.config-item-content {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  justify-content: flex-end;
}

.config-item-value {
  color: #666;
  font-size: 14px;
  word-break: break-all;
}

.password-wrapper {
  display: flex;
  align-items: center;
  gap: 8px;
}

.mod-tag {
  margin: 2px;
  padding: 0 3px;
}
</style>
