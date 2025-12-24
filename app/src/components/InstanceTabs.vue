<template>
  <div class="instance-tabs-wrapper">
    <div class="instance-tags">
      <div
          v-for="tab in tabs"
          :key="tab.key"
          :class="['instance-tag', { 'active': tab.key === activeKey }]"
          @click="handleTagClick(tab)"
      >
        <span class="tag-text">{{ `Server: ${tab.title}` }}</span>
        <span class="tag-close" @click.stop="handleDelete(tab.key)">
          <icon-close-circle/>
        </span>
      </div>
      <div
          v-for="tab in tabs"
          :key="tab.key"
          :class="['instance-tag', { 'active': tab.key === activeKey }]"
          @click="handleTagClick(tab)"
      >
        <span class="tag-text">{{ `Server: ${tab.title}` }}</span>
        <span class="tag-close" @click.stop="handleDelete(tab.key)">
          <icon-close-circle/>
        </span>
      </div>
      <div
          v-for="tab in tabs"
          :key="tab.key"
          :class="['instance-tag', { 'active': tab.key === activeKey }]"
          @click="handleTagClick(tab)"
      >
        <span class="tag-text">{{ `Server: ${tab.title}` }}</span>
        <span class="tag-close" @click.stop="handleDelete(tab.key)">
          <icon-close-circle/>
        </span>
      </div>
      <div
          v-for="tab in tabs"
          :key="tab.key"
          :class="['instance-tag', { 'active': tab.key === activeKey }]"
          @click="handleTagClick(tab)"
      >
        <span class="tag-text">{{ `Server: ${tab.title}` }}</span>
        <span class="tag-close" @click.stop="handleDelete(tab.key)">
          <icon-close-circle/>
        </span>
      </div>
      <div
          v-for="tab in tabs"
          :key="tab.key"
          :class="['instance-tag', { 'active': tab.key === activeKey }]"
          @click="handleTagClick(tab)"
      >
        <span class="tag-text">{{ `Server: ${tab.title}` }}</span>
        <span class="tag-close" @click.stop="handleDelete(tab.key)">
          <icon-close-circle/>
        </span>
      </div>
      <div
          v-for="tab in tabs"
          :key="tab.key"
          :class="['instance-tag', { 'active': tab.key === activeKey }]"
          @click="handleTagClick(tab)"
      >
        <span class="tag-text">{{ `Server: ${tab.title}` }}</span>
        <span class="tag-close" @click.stop="handleDelete(tab.key)">
          <icon-close-circle/>
        </span>
      </div>
    </div>
  </div>
</template>

<script setup>
import {ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'

// const tabs = defineModel('tabs', {
//   default: () => {
//     const cache = localStorage.getItem('instanceTabs')
//     return cache ? JSON.parse(cache) : []
//   }
// })

const tabs = ref(getCacheTabs())
const emit = defineEmits(['close', 'change'])

const route = useRoute()
const router = useRouter()
const activeKey = ref('')

watch(() => tabs, (newTabs) => {
  console.log(JSON.stringify(newTabs.value))
  localStorage.setItem("instanceTabs", JSON.stringify(newTabs.value))
}, {deep: true})

watch(() => route.path, (newPath) => {
  const matchedTab = tabs.value.find(tab => tab.path === newPath)
  if (matchedTab) {
    activeKey.value = matchedTab.key
  } else {
    activeKey.value = ""
  }
}, {immediate: true})

function getCacheTabs() {
  const cache = localStorage.getItem('instanceTabs')
  return cache ? JSON.parse(cache) : []
}

const handleDelete = (key) => {
  const index = tabs.value.findIndex(tab => tab.key === key)
  if (index !== -1) {
    tabs.value.splice(index, 1)
    if (route.path === key) {
      if (tabs.value.length > 0) {
        const newTab = tabs.value[Math.min(index, tabs.value.length - 1)]
        gotoPage(newTab)
      } else {
        router.push('/')
      }
    }
  }
  emit('close', key)
}

const addTab = (title, path, name, params) => {
  const existingTab = tabs.value.find(tab => tab.path === path)
  if (!existingTab) {
    tabs.value.push({
      key: path,
      title: title,
      path: path,
      name: name,
      params: params
    })
    console.log(tabs.value)
  }
  router.push({
    name: name,
    params: {
      ...params
    }
  })
}

const handleTagClick = (tab) => {
  gotoPage(tab)
  emit('change', tab)
}

function gotoPage(tab) {
  router.push({
    name: tab.name,
    params: {
      ...tab.params
    }
  })
}

defineExpose({
  addTab
})

</script>

<style scoped lang="less">
.instance-tabs-wrapper {
  width: 100%;
  background-color: #fff;
  height: 100%;

  .instance-tags {
    width: 100%;
    padding: 6px;
    display: flex;
    flex-wrap: nowrap;
    column-gap: 6px;
    row-gap: 3px;
    align-items: center;
    overflow: hidden;
    height: 100%;
    box-sizing: border-box;

    &::-webkit-scrollbar {
      width: 6px;
      height: 6px;
    }

    &::-webkit-scrollbar-thumb {
      background-color: #c9cdd4;
      border-radius: 3px;
    }

    &::-webkit-scrollbar-track {
      background-color: #f2f3f5;
    }

    .instance-tag {
      display: inline-flex;
      align-items: center;
      cursor: pointer;
      font-size: 13px;
      padding: 0 8px;
      box-sizing: border-box;
      border-radius: 4px;
      background-color: #f2f3f5;
      color: #1d2129;
      border: 1px solid #e5e6eb;
      transition: all 0.2s;
      user-select: none;
      flex-shrink: 0;
      height: 80%;
      box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);

      &:hover {
        background-color: #e8f3ff;
        border-color: #165dff;
        color: #165dff;
      }

      &.active {
        background-color: #165dff;
        color: #fff;
        border-color: #165dff;
      }

      .tag-text {
        margin-right: 4px;
      }

      .tag-close {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 16px;
        height: 16px;
        border-radius: 50%;
        font-size: 14px;
        line-height: 1;
        opacity: 0.6;
        transition: opacity 0.2s;

        &:hover {
          opacity: 1;
          background-color: rgba(0, 0, 0, 0.1);
        }
      }
    }
  }
}
</style>
