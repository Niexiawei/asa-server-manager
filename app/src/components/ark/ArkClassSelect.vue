<template>
  <t-select
      :value="modelValue || undefined"
      :options="options"
      filterable
      creatable
      clearable
      :filter="filterMethod"
      :loading="loading"
      :scroll="{ type: 'virtual' }"
      :placeholder="placeholder"
      :popup-props="{ overlayInnerStyle: { maxHeight: '320px' } }"
      @change="onChange"
      @create="onCreate"
  >
    <template #valueDisplay="data">
      <div class="ark-option-row">
        <t-image class="ark-option-icon" :src="data.iconUrl"
          :fallback="iconFallback"
        ></t-image>
        <span>{{ data.label }}</span>
      </div>
    </template>
  </t-select>
</template>

<script setup>
import {computed, ref, watch} from 'vue'
import iconFallback from '@/assets/static_404.png?url'

const props = defineProps({
  modelValue: {type: String, default: ''},
  dataset: {type: String, default: 'items'}, // 'items' | 'creatures' | 'engrams'
  placeholder: {type: String, default: '搜索名称或 ClassName'},
})
const emit = defineEmits(['update:modelValue'])

// 模块级缓存，多个实例共享同一份数据
const datasetCache = {}

const loading = ref(false)
const records = ref([])

const loadDataset = async (name) => {
  if (datasetCache[name]) {
    records.value = datasetCache[name]
    return
  }
  loading.value = true
  try {
    const mod = name === 'creatures'
        ? await import('@/data/ark-creatures.json')
        : name === 'engrams'
            ? await import('@/data/ark-engrams.json')
            : await import('@/data/ark-items.json')
    datasetCache[name] = mod.default || mod
    records.value = datasetCache[name]
  } catch (e) {
    console.error('加载 ARK 数据集失败:', e)
    records.value = []
  } finally {
    loading.value = false
  }
}

watch(() => props.dataset, (name) => loadDataset(name), {immediate: true})

const options = computed(() => {
  const ds = props.dataset
  const list = records.value.map((r) => {
    let iconUrl = null
    if (r.rawName) {
      if (ds === 'creatures') {
        iconUrl = `http://127.0.0.1:19193/api/icons/creature?name=${encodeURIComponent(r.rawName)}`
      } else if (ds === 'items') {
        iconUrl = `http://127.0.0.1:19193/api/icons/items?name=${encodeURIComponent(r.rawName)}`
      } else {
        iconUrl = `http://127.0.0.1:19193/api/icons/items?name=${encodeURIComponent(r.rawName)}`
      }
    }
    return {
      label: `${r.name} (${r.className})`,
      value: r.className,
      name: r.name,
      className: r.className,
      iconUrl,
    }
  })
  // 若当前值不在数据集中（Mod 物种或文件已有的自定义 ClassName），补一个占位项保证可回显
  const cur = props.modelValue
  if (cur && !list.some((o) => o.value === cur)) {
    list.unshift({label: cur, value: cur, name: cur, className: cur, iconUrl: null})
  }
  return list
})

const filterMethod = (words, option) => {
  if (!words) return true
  const w = String(words).toLowerCase()
  return (
      String(option.name || '').toLowerCase().includes(w) ||
      String(option.className || option.value || '').toLowerCase().includes(w)
  )
}

const onChange = (val) => emit('update:modelValue', val || '')
const onCreate = (val) => emit('update:modelValue', String(val).trim())
</script>

<style scoped>
.ark-option-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.ark-option-icon {
  width: 20px;
  height: 20px;
  object-fit: contain;
  flex-shrink: 0;
}
</style>
