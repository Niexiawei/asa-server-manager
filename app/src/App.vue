<template>
  <div class="main-body">
    <div class="_content">
      <a-layout-header>
        <div class="header-content">
          <h1>ARK Server Ascended 管理面板</h1>
          <div class="header-bottom">
            <a-menu mode="horizontal" :selected-keys="[currentRoute]" @menu-item-click="handleMenuClick">
              <a-menu-item key="manager">
                <span>服务器管理</span>
              </a-menu-item>
              <a-menu-item key="control">
                <span>服务器控制</span>
              </a-menu-item>
              <a-menu-item key="frp-manager">
                <span>FRP管理</span>
              </a-menu-item>
              <a-menu-item key="system-logs">
                <span>系统日志</span>
              </a-menu-item>
            </a-menu>
            <!-- 右侧工具栏 -->
            <div class="header-tools">
              <!-- 服务器资源占用气泡 -->
              <ServerResourceMonitor/>
              <!-- WebSocket 事件通知组件 -->
              <WSEventNotification/>
            </div>
          </div>
        </div>
      </a-layout-header>
      <a-layout-content>
        <div class="content-wrapper">
          <router-view/>
        </div>
      </a-layout-content>
    </div>
  </div>
</template>

<script setup>
import {ref, watch} from 'vue';
import {useRouter, useRoute} from "vue-router";
import WSEventNotification from '@/components/WSEventNotification.vue';
import ServerResourceMonitor from '@/components/ServerResourceMonitor.vue';
import "@/app.less"

const router = useRouter()
const route = useRoute()

const currentRoute = ref('manager');

watch(() => route.path, (newPath) => {
  if (newPath === '/') {
    currentRoute.value = 'manager';
  } else if (newPath === '/control') {
    currentRoute.value = 'control';
  } else if (newPath === '/system-logs') {
    currentRoute.value = 'system-logs';
  } else if (newPath === '/frp-manager') {
    currentRoute.value = 'frp-manager';
  }
}, {immediate: true});

const handleMenuClick = (key) => {
  currentRoute.value = key;
  switch (key) {
    case "manager":
      router.push({
        path: '/'
      })
      break
    case "control":
      router.push({
        path: '/control'
      })
      break
    case "system-logs":
      router.push({
        path: '/system-logs'
      })
      break
    case "frp-manager":
      router.push({
        path: '/frp-manager'
      })
      break
  }
};
</script>

<style lang="less">
.main-body {
  width: 100%;
  height: 100vh;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  display: flex;
  flex-direction: column;
  overflow: hidden;

  ._content {
    border-radius: 8px;
    overflow: hidden;
    flex: 1;
    display: flex;
    flex-direction: column;

    .arco-layout-content {
      height: calc(100% - 128px);
    }
  }
}

.header-content {
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  width: 100%;

  .header-bottom {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background-color: #fff;
  }

  .header-tools {
    display: flex;
    align-items: center;
    gap: 8px;
  }
}

.header-content h1 {
  color: #ffffff;
  text-align: left;
  text-indent: 15px;
}

.content-wrapper {
  margin: 0 auto;
  padding: 10px;
  width: 100%;
  height: 100%;
  box-sizing: border-box;
  background-color: #eeeeee;
}

.arco-layout-header {
  background-color: #2c3e50;
  width: 100%;
  position: relative;
}
</style>