<template>
  <div class="backup-tab">
    <div class="backup-actions">
      <t-button theme="primary" size="small" :loading="backupLoading" @click="createBackupHandler">
        创建备份
      </t-button>
      <t-button size="small" :loading="backupListLoading" @click="fetchBackups">刷新</t-button>
    </div>

    <t-loading :loading="backupListLoading">
      <div v-if="backups.length === 0 && !backupListLoading" class="backup-empty">暂无备份</div>
      <t-row :gutter="[12, 12]">
        <t-col :span="4" v-for="item in backups" :key="item">
          <t-card size="small" :bordered="true">
            <template #header>
              <div class="backup-card-head">
                <t-tag v-if="item.endsWith('_latest.zstd')" theme="warning" size="small">latest</t-tag>
                <t-tag v-else theme="default" size="small">备份</t-tag>
              </div>
            </template>
            <div class="backup-name">{{ item }}</div>
            <t-space size="small">
              <t-button size="small" theme="primary" @click="restoreBackupHandler(item)">恢复</t-button>
              <t-button size="small" theme="danger" variant="outline" @click="deleteBackupHandler(item)">
                删除
              </t-button>
            </t-space>
          </t-card>
        </t-col>
      </t-row>
    </t-loading>
  </div>
</template>

<script setup>
import {onMounted, ref} from 'vue'
import {MessagePlugin, DialogPlugin} from 'tdesign-vue-next'
import {
  createWorldBackup,
  listWorldBackups,
  restoreWorldBackup,
  deleteWorldBackup,
} from '@/apis/api.js'

const props = defineProps({
  instanceName: {type: String, required: true},
})

const backups = ref([])
const backupLoading = ref(false)
const backupListLoading = ref(false)

const fetchBackups = async () => {
  backupListLoading.value = true
  try {
    const data = await listWorldBackups(props.instanceName)
    if (data.success) {
      backups.value = data.data?.backups || []
    } else {
      MessagePlugin.error(data.error || '获取备份列表失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '获取备份列表失败')
  } finally {
    backupListLoading.value = false
  }
}

const createBackupHandler = async () => {
  backupLoading.value = true
  try {
    const data = await createWorldBackup(props.instanceName)
    if (data.success) {
      MessagePlugin.success('备份创建成功')
      await fetchBackups()
    } else {
      MessagePlugin.error(data.error || '创建备份失败')
    }
  } catch (err) {
    MessagePlugin.error(err.message || '创建备份失败')
  } finally {
    backupLoading.value = false
  }
}

const restoreBackupHandler = (filename) => {
  const isLatest = filename.endsWith('_latest.zstd')
  const label = isLatest ? '「latest 快照」' : `"${filename}"`
  const d = DialogPlugin.confirm({
    header: '确认恢复备份',
    body: `确定要将存档恢复为 ${label} 吗？\n\n恢复前系统将自动把当前存档保存为 latest 快照。\n\n此操作将覆盖当前存档数据，不可撤销，请谨慎操作！`,
    theme: 'warning',
    confirmBtn: {content: '确认恢复', theme: 'warning'},
    cancelBtn: '取消',
    onConfirm: async () => {
      d.hide()
      try {
        const data = await restoreWorldBackup(props.instanceName, filename)
        if (data.success) {
          MessagePlugin.success('备份恢复成功，latest 快照已更新')
          await fetchBackups()
        } else {
          MessagePlugin.error(data.error || '恢复失败')
        }
      } catch (err) {
        MessagePlugin.error(err.message || '恢复失败')
      }
    },
  })
}

const deleteBackupHandler = (filename) => {
  const isLatest = filename.endsWith('_latest.zstd')
  const d = DialogPlugin.confirm({
    header: '确认删除备份',
    body: isLatest
        ? `确定要删除 latest 快照吗？\n\n该快照是恢复操作前自动保存的安全备份，删除后无法找回！`
        : `确定要删除备份 "${filename}" 吗？\n\n此操作不可撤销！`,
    theme: 'danger',
    confirmBtn: {content: '确认删除', theme: 'danger'},
    cancelBtn: '取消',
    onConfirm: async () => {
      d.hide()
      try {
        const data = await deleteWorldBackup(props.instanceName, filename)
        if (data.success) {
          MessagePlugin.success('备份已删除')
          await fetchBackups()
        } else {
          MessagePlugin.error(data.error || '删除失败')
        }
      } catch (err) {
        MessagePlugin.error(err.message || '删除失败')
      }
    },
  })
}

onMounted(fetchBackups)
</script>

<style scoped lang="less">
.backup-tab {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.backup-actions {
  display: flex;
  gap: 8px;
}

.backup-empty {
  color: #999;
  padding: 16px 0;
}

.backup-card-head {
  display: flex;
  align-items: center;
  gap: 6px;
}

.backup-name {
  font-size: 12px;
  word-break: break-all;
  color: #555;
  margin-bottom: 8px;
}
</style>
