<template>
  <div class="main-body">
    <div class="_content">
      <a-layout-header>
        <div class="header-content">
          <div class="icon">
            <img class="img" src="/ASA_Logo_transparent.webp">
            <div class="title-text">Ascended Server</div>
          </div>
          <div class="header-bottom">
            <a-menu mode="horizontal" class="menu-content" :selected-keys="[currentRoute]"
                    @menu-item-click="handleMenuClick">
              <a-menu-item key="manager">
                <span>服务器管理</span>
              </a-menu-item>
              <a-menu-item key="control">
                <span>服务器控制</span>
              </a-menu-item>
              <a-menu-item key="frp-manager">
                <span>FRP管理</span>
              </a-menu-item>
              <a-menu-item key="syncthing-manager">
                <span>Syncthing管理</span>
              </a-menu-item>
              <a-menu-item key="system-logs">
                <span>系统日志</span>
              </a-menu-item>
            </a-menu>
            <div class="header-middle">
              <instance-tabs
                  ref="InstanceTabRef"
                  @close="handleTabClose"
                  @change="handleTabChange"
              />
            </div>
            <div class="header-tools">
              <ServerResourceMonitor/>
              <WSEventNotification/>
            </div>
          </div>
        </div>
      </a-layout-header>
      <a-layout-content>
        <div class="content-wrapper">
          <router-view :key="$route.fullPath"/>
        </div>
      </a-layout-content>
    </div>
  </div>
</template>

<script setup>
import {ref, watch, computed, provide} from 'vue';
import {useRouter, useRoute} from "vue-router";
import WSEventNotification from '@/components/WSEventNotification.vue';
import ServerResourceMonitor from '@/components/ServerResourceMonitor.vue';
import InstanceTabs from '@/components/InstanceTabs.vue';
import "@/app.less"

const router = useRouter()
const route = useRoute()
const InstanceTabRef = ref()
const currentRoute = ref('manager');

const addTab = (title, path, name, params) => {
  InstanceTabRef.value.addTab(title, path, name, params);
}

provide('addTab', addTab)

watch(() => route.path, (newPath) => {
  if (newPath === '/') {
    currentRoute.value = 'manager';
  } else if (newPath === '/control') {
    currentRoute.value = 'control';
  } else if (newPath === '/system-logs') {
    currentRoute.value = 'system-logs';
  } else if (newPath === '/frp-manager') {
    currentRoute.value = 'frp-manager';
  } else if (newPath === '/syncthing-manager') {
    currentRoute.value = 'syncthing-manager';
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
    case "syncthing-manager":
      router.push({
        path: '/syncthing-manager'
      })
      break
  }
};

const handleTabClose = (key) => {

}
const handleTabChange = (tab) => {

}

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
  height: 58px;
  width: 100%;
  flex: 0 0 auto;

  .icon {
    padding: 8px;
    box-sizing: border-box;
    line-height: 58px;
    display: flex;
    align-items: center;

    .title-text {
      font-size: 25px;
      text-indent: 15px;
      color: #1d2129;
    }

    > img {
      width: 42px;
    }
  }

  .header-bottom {
    display: flex;
    align-items: center;
    background-color: #fff;
    flex: 1 1 0; /* 占剩余空间，可收缩 */
    min-width: 0; /* 关键：允许收缩，禁止 min-content 阻止收缩 */
    gap: 16px;

    .menu-content {
      flex: 0 0 auto;
    }

    .header-middle {
      display: flex;
      align-items: center;
      overflow: hidden;
      height: 100%;
      flex: 1 1 0; /* 占剩余空间，可收缩 */
      min-width: 0; /* 关键：允许收缩，禁止 min-content 阻止收缩 */
    }

    .header-tools {
      flex: 0 0 auto;
      display: flex;
      align-items: center;
      gap: 8px;
      flex-shrink: 0;
    }
  }
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
  width: 100%;
  position: relative;
}
</style>