<template>
  <div class="main-body">
    <div class="_content">
      <a-layout-header>
        <div class="header-content">
          <h1>ARK Server Ascended 管理面板</h1>
          <a-menu mode="horizontal" :selected-keys="[currentRoute]" @menu-item-click="handleMenuClick">
            <a-menu-item key="manager">
              <span>服务器管理</span>
            </a-menu-item>
            <a-menu-item key="control">
              <span to="/control">服务器控制</span>
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
import {useRouter} from "vue-router";

const router = useRouter()

const currentRoute = ref('manager');

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
  }
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
