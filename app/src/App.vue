<template>
  <div class="main-body">
    <div class="_content">
      <div class="header-content">
        <div class="icon">
          <img class="img" src="/ASA_Logo_transparent.webp">
        </div>
        <div class="header-bottom">
          <div class="menu-content">
            <t-head-menu mode="horizontal" :value="currentRoute" @change="handleMenuClick">
              <t-menu-item value="manager">
                <span>服务器管理</span>
              </t-menu-item>
              <t-menu-item value="frp-manager">
                <span>FRP管理</span>
              </t-menu-item>
              <t-menu-item value="syncthing-manager">
                <span>Syncthing管理</span>
              </t-menu-item>
              <t-menu-item value="schedule-manager">
                <span>定时任务</span>
              </t-menu-item>
              <t-menu-item value="system-logs">
                <span>系统日志</span>
              </t-menu-item>
            </t-head-menu>
          </div>
          <div class="header-middle">
            <instance-tabs
                ref="InstanceTabRef"
                @close="handleTabClose"
                @change="handleTabChange"
            />
          </div>
          <div class="header-tools">
            <WSStatusIndicator/>
            <ServerResourceMonitor/>
            <WSEventNotification/>
          </div>
        </div>
      </div>
      <div class="content-wrapper"
           ref="contentWrapperRef"
      >
        <router-view v-slot="{Component,route}">
          <KeepAlive :include="[
                  'SystemLogs',
                  'ServerManager'
              ]">
            <component class="layout-card-wrapper" :is="Component"
                       :key="route.name === 'InstanceDetail' ? route.fullPath : route.name">
            </component>
          </KeepAlive>
        </router-view>
      </div>
    </div>
  </div>
</template>

<script setup>
import {ref, watch, computed, provide, useTemplateRef} from 'vue';
import {useRouter, useRoute} from "vue-router";
import WSEventNotification from '@/components/WSEventNotification.vue';
import ServerResourceMonitor from '@/components/ServerResourceMonitor.vue';
import WSStatusIndicator from '@/components/WSStatusIndicator.vue';
import InstanceTabs from '@/components/InstanceTabs.vue';
import {useElementSize} from "@vueuse/core";

const router = useRouter()
const route = useRoute()
const InstanceTabRef = ref()
const currentRoute = ref('manager');
const el = useTemplateRef('contentWrapperRef')
const {width, height} = useElementSize(el)


const addTab = (title, path, name, params) => {
  InstanceTabRef.value.addTab(title, path, name, params);
}

provide('addTab', addTab)

watch(() => route.path, (newPath) => {
  if (newPath === '/') {
    currentRoute.value = 'manager';
  } else if (newPath === '/system-logs') {
    currentRoute.value = 'system-logs';
  } else if (newPath === '/frp-manager') {
    currentRoute.value = 'frp-manager';
  } else if (newPath === '/syncthing-manager') {
    currentRoute.value = 'syncthing-manager';
  } else if (newPath === '/schedule-manager') {
    currentRoute.value = 'schedule-manager';
  } else {
    currentRoute.value = "";
  }
}, {immediate: true});

const handleMenuClick = (value) => {
  currentRoute.value = value;
  switch (value) {
    case "manager":
      router.push({
        path: '/'
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
    case "schedule-manager":
      router.push({
        path: '/schedule-manager'
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

</style>

<style lang="less" scoped>

.slide-fade-enter-active {
  transition: all 0.3s ease-out;
}

.slide-fade-leave-active {
  transition: all 0.8s cubic-bezier(1, 0.5, 0.8, 1);
}

.slide-fade-enter-from,
.slide-fade-leave-to {
  transform: translateX(20px);
  opacity: 0;
}

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

    .t-layout__content {
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
  background: #fff;

  .icon {
    padding: 4px;
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
      width: 52px;
    }
  }

  .header-bottom {
    display: flex;
    align-items: center;
    background-color: #fff;
    flex: 1 1 0; /* 占剩余空间，可收缩 */
    min-width: 0; /* 关键：允许收缩，禁止 min-content 阻止收缩 */
    gap: 16px;

    :deep(.t-menu__inner) {
      padding: 14px 6px;
    }

    .menu-content {
      width: 610px;
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
  height: calc(100% - 58px);
  box-sizing: border-box;
  background-color: #eeeeee;
}

.t-layout__header {
  width: 100%;
  position: relative;
}
</style>
