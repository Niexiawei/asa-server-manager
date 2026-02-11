<template>
  <!-- RCON 命令 -->
  <t-card title="RCON 命令" :bordered="false" class="section-card">
    <t-form :data="rconForm" layout="vertical">
      <t-row :gutter="20">
        <t-col :span="12">
          <t-form-item name="instance" label="选择实例">
            <t-select 
              v-model="rconForm.instance" 
              placeholder="请选择实例"
            >
              <t-option value="">请选择实例</t-option>
              <t-option 
                v-for="instance in instances" 
                :key="instance.name" 
                :value="instance.name"
              >
                {{ instance.name }}
              </t-option>
            </t-select>
          </t-form-item>
        </t-col>
        
        <t-col :span="12">
          <t-form-item name="command" label="RCON 命令">
            <t-input 
              v-model="rconForm.command" 
              placeholder="输入 RCON 命令"
              @keyup.enter="sendRconCommandHandler"
            />
          </t-form-item>
        </t-col>
      </t-row>
      
      <t-form-item>
        <t-button 
          @click="sendRconCommandHandler" 
          theme="primary"
          :disabled="!rconForm.instance || !rconForm.command"
        >
          发送命令
        </t-button>
      </t-form-item>
    </t-form>
    
    <div class="rcon-response" v-if="rconResponse">
      <h4>响应:</h4>
      <t-alert theme="info">
        <pre>{{ rconResponse }}</pre>
      </t-alert>
    </div>
  </t-card>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { 
  sendRCONCommand
} from '@/apis/api.js'
import { MessagePlugin } from 'tdesign-vue-next'

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
      MessagePlugin.success('命令发送成功')
    } else {
      console.error('发送 RCON 命令失败:', data.error)
      rconResponse.value = `错误: ${data.error}`
      MessagePlugin.error('发送 RCON 命令失败: ' + (data.error || '未知错误'))
    }
  } catch (error) {
    console.error('发送 RCON 命令失败:', error)
    rconResponse.value = `错误: ${error.message}`
    MessagePlugin.error('发送 RCON 命令失败: ' + error.message)
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

:deep(.t-alert--info) {
  background-color: #f0f9ff;
  border-color: #337ecc;
}

:deep(pre) {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
