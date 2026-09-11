<template>
  <div class="result-list">
    <div v-for="r in results" :key="r.instance" class="result-row">
      <t-tag :theme="r.ok ? 'success' : 'danger'" variant="light" size="small">{{ r.ok ? '成功' : '失败' }}</t-tag>
      <div class="body">
        <div>
          <span class="inst">{{ r.instance }}</span>
          <span v-if="r.ok" class="muted">{{ actionText[r.action] ?? r.action }}</span>
          <span v-else class="err">{{ r.error }}</span>
        </div>
        <div v-for="(w, i) in r.warnings || []" :key="i" class="warn">{{ w }}</div>
      </div>
    </div>
  </div>
</template>

<script setup>
// 插件安装 / 卸载的逐实例结果。一个实例失败不影响其他实例，所以每个实例单独一行。
defineProps({
  results: {type: Array, default: () => []}
})

const actionText = {install: '已新装', update: '已更新', uninstall: '已卸载（配置与数据已移入备份）'}
</script>

<style scoped>
.result-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.result-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.inst {
  font-weight: 500;
  margin-right: 8px;
}

.muted {
  color: var(--td-text-color-secondary);
}

.err {
  color: var(--td-error-color);
}

.warn {
  font-size: 12px;
  color: var(--td-warning-color);
}
</style>
