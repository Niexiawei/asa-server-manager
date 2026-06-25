<template>
  <ark-class-select
      :model-value="modelValue"
      dataset="engrams"
      placeholder="搜索印痕中文名或 ClassName"
      @update:model-value="(v) => emit('update:modelValue', v)"
  />
</template>

<script setup>
import ArkClassSelect from './ArkClassSelect.vue'

defineProps({modelValue: {type: String, default: ''}})
const emit = defineEmits(['update:modelValue'])
</script>
