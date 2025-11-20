<template>
  <!-- RCON 命令 -->
  <a-card title="RCON 命令" :bordered="false" class="section-card">
    <a-form :model="rconForm" layout="vertical">
      <a-row :gutter="20">
        <a-col :span="12">
          <a-form-item field="instance" label="选择实例">
            <a-select 
              v-model="rconForm.instance" 
              placeholder="请选择实例"
            >
              <a-option value="">请选择实例</a-option>
              <a-option 
                v-for="instance in instances" 
                :key="instance.name" 
                :value="instance.name"
              >
                {{ instance.name }}
              </a-option>
            </a-select>
          </a-form-item>
        </a-col>
        
        <a-col :span="12">
          <a-form-item field="command" label="RCON 命令">
            <a-input 
              v-model="rconForm.command" 
              placeholder="输入 RCON 命令"
              @press-enter="sendRconCommandHandler"
            />
          </a-form-item>
        </a-col>
      </a-row>
      
      <a-form-item>
        <a-button 
          @click="sendRconCommandHandler" 
          type="primary"
          :disabled="!rconForm.instance || !rconForm.command"
        >
          发送命令
        </a-button>
      </a-form-item>
    </a-form>
    
    <div class="rcon-response" v-if="rconResponse">
      <h4>响应:</h4>
      <a-alert type="info">
        <pre>{{ rconResponse }}</pre>
      </a-alert>
    </div>
  </a-card>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { 
  sendRCONCommand
} from '@/apis/api.js'
import { Message } from '@arco-design/web-vue'

const props = defineProps({
  instances: {
    type: Array,
    default: () => []
  }
})

// 状态管理
const rconResponse = ref('')
const rconForm = reactive({
  instance: '',
  command: ''
})

// 发送 RCON 命令
const sendRconCommandHandler = async () => {
  if (!rconForm.instance || !rconForm.command) return
  
  try {
    const data = await sendRCONCommand(rconForm.instance, rconForm.command)
    if (data.success) {
      rconResponse.value = data.data.response
      Message.success('命令发送成功')
    } else {
      console.error('发送 RCON 命令失败:', data.error)
      rconResponse.value = `错误: ${data.error}`
      Message.error('发送 RCON 命令失败: ' + (data.error || '未知错误'))
    }
  } catch (error) {
    console.error('发送 RCON 命令失败:', error)
    rconResponse.value = `错误: ${error.message}`
    Message.error('发送 RCON 命令失败: ' + error.message)
  }
}
</script>

<style scoped>
.section-card {
  margin-top: 20px;
}

.rcon-response h4 {
  margin: 20px 0 10px 0;
}

:deep(.arco-alert-info) {
  background-color: #f0f9ff;
  border-color: #337ecc;
}

:deep(pre) {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
