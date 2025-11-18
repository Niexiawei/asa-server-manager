<template>
  <div class="api-docs">
    <h2>API 接口文档</h2>
    
    <div class="endpoint">
      <h3>获取实例列表</h3>
      <div class="method">GET</div>
      <div class="url">/api/instances</div>
      <div class="description">获取所有服务器实例的信息</div>
      <div class="response">
        <h4>响应示例:</h4>
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
      </div>
    </div>
    
    <div class="endpoint">
      <h3>创建新实例</h3>
      <div class="method">POST</div>
      <div class="url">/api/instances</div>
      <div class="description">创建一个新的服务器实例</div>
      <div class="request">
        <h4>请求参数:</h4>
        <pre><code>{
  "name": "实例名称"
}</code></pre>
      </div>
      <div class="response">
        <h4>响应示例:</h4>
        <pre><code>{
  "success": true,
  "message": "Instance '实例名称' created successfully"
}</code></pre>
      </div>
    </div>
    
    <div class="endpoint">
      <h3>启动服务器实例</h3>
      <div class="method">POST</div>
      <div class="url">/api/server/:name/start</div>
      <div class="description">启动指定名称的服务器实例</div>
      <div class="response">
        <h4>响应示例:</h4>
        <pre><code>{
  "success": true,
  "message": "Server '实例名称' started successfully"
}</code></pre>
      </div>
    </div>
    
    <div class="endpoint">
      <h3>停止服务器实例</h3>
      <div class="method">POST</div>
      <div class="url">/api/server/:name/stop</div>
      <div class="description">停止指定名称的服务器实例</div>
      <div class="response">
        <h4>响应示例:</h4>
        <pre><code>{
  "success": true,
  "message": "Server '实例名称' stopped successfully"
}</code></pre>
      </div>
    </div>
    
    <div class="endpoint">
      <h3>删除实例</h3>
      <div class="method">DELETE</div>
      <div class="url">/api/instances/:name</div>
      <div class="description">删除指定名称的服务器实例</div>
      <div class="response">
        <h4>响应示例:</h4>
        <pre><code>{
  "success": true,
  "message": "Instance '实例名称' deleted successfully"
}</code></pre>
      </div>
    </div>
  </div>
</template>

<script setup>
// API 文档组件不需要额外的逻辑
</script>

<style scoped>
.api-docs {
  max-width: 800px;
  margin: 0 auto;
  padding: 20px;
  text-align: left;
}

.endpoint {
  border: 1px solid #ddd;
  border-radius: 8px;
  padding: 20px;
  margin-bottom: 20px;
  background-color: #f9f9f9;
}

.method {
  display: inline-block;
  padding: 4px 8px;
  background-color: #42b883;
  color: white;
  border-radius: 4px;
  font-weight: bold;
  margin-right: 10px;
}

.url {
  display: inline-block;
  font-family: monospace;
  background-color: #f0f0f0;
  padding: 4px 8px;
  border-radius: 4px;
}

.description {
  margin: 10px 0;
  color: #666;
}

.response, .request {
  margin-top: 15px;
}

.response h4, .request h4 {
  margin-top: 15px;
  margin-bottom: 5px;
}

pre {
  background-color: #f0f0f0;
  padding: 10px;
  border-radius: 4px;
  overflow-x: auto;
}

code {
  font-family: 'Courier New', monospace;
}
</style>