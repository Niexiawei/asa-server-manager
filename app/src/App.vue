<template>
  <div class="main-body">
    <div class="_content">
      <a-layout-header>
        <div class="header-content">
          <h1>ARK Server Ascended 管理面板</h1>
          <a-menu mode="horizontal" :selected-keys="[currentRoute]" @menu-item-click="handleMenuClick">
            <a-menu-item key="manager">
              <router-link to="/">服务器管理</router-link>
            </a-menu-item>
            <a-menu-item key="control">
              <router-link to="/control">服务器控制</router-link>
            </a-menu-item>
          </a-menu>
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
import {useRoute, useRouter} from 'vue-router';

const route = useRoute();
const router = useRouter();
const currentRoute = ref('manager');

// 监听路由变化，更新选中的菜单项
watch(() => route.path, (newPath) => {
  if (newPath === '/') {
    currentRoute.value = 'manager';
  } else if (newPath === '/control') {
    currentRoute.value = 'control';
  } else if (newPath === '/api-docs') {
    currentRoute.value = 'api-docs';
  }
}, {immediate: true});

const handleMenuClick = (key) => {
  currentRoute.value = key;
};
</script>

<style>
.main-body {
  width: 100%;
  height: 100vh;
  padding: 20px;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  box-sizing: border-box;

  ._content {
    border-radius: 8px;
    overflow: hidden;
    height: 100%;
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
}

.header-content h1 {
  color: #ffffff;
  text-align: left;
  text-indent: 15px;
}

.content-wrapper {
  margin: 0 auto;
  padding: 20px;
  width: 100%;
  height: 100%;
  box-sizing: border-box;
  background-color: #eeeeee;
}

.arco-layout-header {
  background-color: #2c3e50;
  width: 100%;
}
</style>
