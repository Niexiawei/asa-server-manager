# ASA Server Manager 前端

这是一个基于 Vue 3 和 Vite 构建的前端管理界面，用于管理 ARK: Survival Ascended 服务器实例。

## 功能特性

- 服务器实例管理（创建、启动、停止、删除）
- 实时状态监控
- API 接口文档

## 技术栈

- Vue 3 (Composition API)
- Vite
- Vue Router

## 开发

### 启动开发服务器

```bash
npm run dev
```

开发服务器将在 http://localhost:3000 上运行。

### 构建生产版本

```bash
npm run build
```

### 预览生产构建

```bash
npm run preview
```

## 项目结构

```
src/
├── components/          # Vue 组件
├── router/              # 路由配置
├── App.vue              # 根组件
└── main.js              # 入口文件
```

## API 接口

前端通过 REST API 与后端服务通信。API 文档可以在应用内的"API 文档"页面查看。