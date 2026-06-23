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
  />
</template>

<script setup>
import {computed, ref, watch} from 'vue'

const props = defineProps({
  modelValue: {type: String, default: ''},
  dataset: {type: String, default: 'items'}, // 'items' | 'creatures'
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
  const list = records.value.map((r) => ({
    // 当前 TDesign 版本不支持顶层 option 插槽，故把名称与 ClassName 合并到 label 里展示
    label: `${r.name} (${r.className})`,
    value: r.className,
    name: r.name,
    className: r.className,
  }))
  // 若当前值不在数据集中（Mod 物种或文件已有的自定义 ClassName），补一个占位项保证可回显
  const cur = props.modelValue
  if (cur && !list.some((o) => o.value === cur)) {
    list.unshift({label: cur, value: cur, name: cur, className: cur})
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
