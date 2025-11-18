<template>
  <div class="api-docs">
    <a-card title="API 接口文档" :bordered="false" class="main-card">
      <a-collapse :default-active-key="['1']" expand-icon-position="right">
        <a-collapse-item header="健康检查" key="1">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="green">GET</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/health</div>
                <div class="description">检查服务器健康状态</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "status": "healthy",
  "service": "asa-server-manager-api"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="获取实例列表" key="2">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="green">GET</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/instances</div>
                <div class="description">获取所有服务器实例的信息</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "Instances retrieved successfully",
  "data": {
    "instances": [
      {
        "name": "TheIsland",
        "running": true
      }
    ],
    "count": 1
  }
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="创建新实例" key="3">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="orangered">POST</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/instances</div>
                <div class="description">创建一个新的服务器实例</div>
              </a-col>
            </a-row>
            <div class="request">
              <h4>请求参数:</h4>
              <a-alert type="info">
                <pre><code>{
  "name": "实例名称"
}</code></pre>
              </a-alert>
            </div>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "Instance '实例名称' created successfully"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="获取实例状态" key="4">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="green">GET</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/instances/:name</div>
                <div class="description">获取指定名称的服务器实例状态</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "Instance status retrieved",
  "data": {
    "name": "TheIsland",
    "running": true,
    "config": {
      // 实例配置信息
    }
  }
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="启动服务器实例" key="5">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="orangered">POST</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/server/:name/start</div>
                <div class="description">启动指定名称的服务器实例</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "Server '实例名称' started successfully"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="停止服务器实例" key="6">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="orangered">POST</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/server/:name/stop</div>
                <div class="description">停止指定名称的服务器实例</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "Server '实例名称' stopped successfully"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="重启服务器实例" key="7">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="orangered">POST</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/server/:name/restart</div>
                <div class="description">重启指定名称的服务器实例</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "Server '实例名称' restarted successfully"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="删除实例" key="8">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="red">DELETE</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/instances/:name</div>
                <div class="description">删除指定名称的服务器实例</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "Instance '实例名称' deleted successfully"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="重命名实例" key="9">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="purple">PUT</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/instances/:name</div>
                <div class="description">重命名指定名称的服务器实例</div>
              </a-col>
            </a-row>
            <div class="request">
              <h4>请求参数:</h4>
              <a-alert type="info">
                <pre><code>{
  "new_name": "新实例名称"
}</code></pre>
              </a-alert>
            </div>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "Instance renamed from '旧名称' to '新名称'"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="启动所有实例" key="10">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="orangered">POST</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/server/start-all</div>
                <div class="description">启动所有服务器实例</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "All servers started successfully"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
        
        <a-collapse-item header="停止所有实例" key="11">
          <a-card :bordered="true" class="endpoint-card">
            <a-row>
              <a-col :span="4">
                <a-tag color="orangered">POST</a-tag>
              </a-col>
              <a-col :span="20">
                <div class="url">http://127.0.0.1:8080/api/server/stop-all</div>
                <div class="description">停止所有服务器实例</div>
              </a-col>
            </a-row>
            <div class="response">
              <h4>响应示例:</h4>
              <a-alert type="info">
                <pre><code>{
  "success": true,
  "message": "All servers stopped successfully"
}</code></pre>
              </a-alert>
            </div>
          </a-card>
        </a-collapse-item>
      </a-collapse>
    </a-card>
  </div>
</template>

<script setup>
// API 文档组件不需要额外的逻辑
</script>

<style scoped>
.api-docs {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.main-card {
  flex: 1;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  overflow: auto;
}

.endpoint-card {
  margin-bottom: 20px;
}

.url {
  font-family: monospace;
  background-color: #f0f0f0;
  padding: 4px 8px;
  border-radius: 4px;
  word-break: break-all;
  margin-bottom: 10px;
}

.description {
  color: #666;
  margin-bottom: 15px;
}

.response, .request {
  margin-top: 15px;
}

.response h4, .request h4 {
  margin-top: 15px;
  margin-bottom: 5px;
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

:deep(.arco-collapse-item) {
  margin-bottom: 10px;
}

:deep(.arco-collapse-item-header) {
  background-color: #f5f5f5;
  border-radius: 4px;
  padding: 12px 16px;
}

:deep(.arco-collapse-item-content) {
  padding: 20px 0;
}
</style>