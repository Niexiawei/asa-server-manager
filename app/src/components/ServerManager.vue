<template>
  <div class="server-manager">
    <h2>服务器实例管理</h2>
    
    <!-- 实例列表 -->
    <div class="instances-section">
      <h3>现有实例</h3>
      <div v-if="loading" class="loading">加载中...</div>
      <div v-else-if="instances.length === 0" class="no-instances">
        暂无实例，请创建新实例
      </div>
      <div v-else class="instances-list">
        <div 
          v-for="instance in instances" 
          :key="instance.name" 
          class="instance-card"
          :class="{ running: instance.running }"
        >
          <h4>{{ instance.name }}</h4>
          <p>状态: {{ instance.running ? '运行中' : '已停止' }}</p>
          <div class="instance-actions">
            <button 
              @click="startInstance(instance.name)"
              :disabled="instance.running"
            >
              启动
            </button>
            <button 
              @click="stopInstance(instance.name)"
              :disabled="!instance.running"
            >
              停止
            </button>
            <button @click="deleteInstance(instance.name)">删除</button>
          </div>
        </div>
      </div>
    </div>
    
    <!-- 创建新实例 -->
    <div class="create-section">
      <h3>创建新实例</h3>
      <form @submit.prevent="createInstance">
        <div class="form-group">
          <label for="instanceName">实例名称:</label>
          <input 
            id="instanceName" 
            v-model="newInstanceName" 
            type="text" 
            required 
            placeholder="输入实例名称"
          />
        </div>
        <button type="submit">创建实例</button>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'

// 状态管理
const instances = ref([])
const loading = ref(false)
const newInstanceName = ref('')

// 获取实例列表
const fetchInstances = async () => {
  loading.value = true
  try {
    // 这里应该调用实际的 API 端点
    // const response = await fetch('/api/instances')
    // const data = await response.json()
    // instances.value = data.instances
    
    // 模拟数据
    instances.value = [
      { name: 'TheIsland', running: true },
      { name: 'Ragnarok', running: false }
    ]
  } catch (error) {
    console.error('获取实例列表失败:', error)
  } finally {
    loading.value = false
  }
}

// 创建实例
const createInstance = async () => {
  if (!newInstanceName.value.trim()) return
  
  try {
    // 这里应该调用实际的 API 端点
    // const response = await fetch('/api/instances', {
    //   method: 'POST',
    //   headers: { 'Content-Type': 'application/json' },
    //   body: JSON.stringify({ name: newInstanceName.value })
    // })
    
    // 添加到本地列表
    instances.value.push({
      name: newInstanceName.value,
      running: false
    })
    
    // 清空输入
    newInstanceName.value = ''
  } catch (error) {
    console.error('创建实例失败:', error)
  }
}

// 启动实例
const startInstance = async (name) => {
  try {
    // 这里应该调用实际的 API 端点
    // const response = await fetch(`/api/server/${name}/start`, {
    //   method: 'POST'
    // })
    
    // 更新本地状态
    const instance = instances.value.find(inst => inst.name === name)
    if (instance) {
      instance.running = true
    }
  } catch (error) {
    console.error('启动实例失败:', error)
  }
}

// 停止实例
const stopInstance = async (name) => {
  try {
    // 这里应该调用实际的 API 端点
    // const response = await fetch(`/api/server/${name}/stop`, {
    //   method: 'POST'
    // })
    
    // 更新本地状态
    const instance = instances.value.find(inst => inst.name === name)
    if (instance) {
      instance.running = false
    }
  } catch (error) {
    console.error('停止实例失败:', error)
  }
}

// 删除实例
const deleteInstance = async (name) => {
  if (!confirm(`确定要删除实例 "${name}" 吗？`)) return
  
  try {
    // 这里应该调用实际的 API 端点
    // const response = await fetch(`/api/instances/${name}`, {
    //   method: 'DELETE'
    // })
    
    // 从本地列表移除
    instances.value = instances.value.filter(inst => inst.name !== name)
  } catch (error) {
    console.error('删除实例失败:', error)
  }
}

// 组件挂载时获取实例列表
onMounted(() => {
  fetchInstances()
})
</script>

<style scoped>
.server-manager {
  max-width: 800px;
  margin: 0 auto;
  padding: 20px;
}

.instances-section, .create-section {
  margin-bottom: 40px;
  text-align: left;
}

.instances-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
  gap: 20px;
}

.instance-card {
  border: 1px solid #ddd;
  border-radius: 8px;
  padding: 15px;
  background-color: #f9f9f9;
}

.instance-card.running {
  border-color: #42b883;
  background-color: #f0fff4;
}

.instance-card h4 {
  margin-top: 0;
  margin-bottom: 10px;
}

.instance-actions {
  margin-top: 15px;
}

.instance-actions button {
  margin-right: 10px;
  padding: 5px 10px;
  border: none;
  border-radius: 4px;
  cursor: pointer;
}

.instance-actions button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.instance-actions button:not(:disabled):hover {
  opacity: 0.8;
}

.form-group {
  margin-bottom: 15px;
}

.form-group label {
  display: block;
  margin-bottom: 5px;
  font-weight: bold;
}

.form-group input {
  width: 100%;
  padding: 8px;
  border: 1px solid #ddd;
  border-radius: 4px;
  box-sizing: border-box;
}

button {
  background-color: #42b883;
  color: white;
  padding: 10px 20px;
  border: none;
  border-radius: 4px;
  cursor: pointer;
}

button:hover:not(:disabled) {
  background-color: #369870;
}

.loading, .no-instances {
  text-align: center;
  padding: 20px;
  color: #666;
}
</style>