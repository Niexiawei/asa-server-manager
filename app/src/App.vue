<template>
  <div id="app">
    <header>
      <h1>ARK Server Ascended 管理面板</h1>
      <nav>
        <router-link to="/">服务器管理</router-link>
        <router-link to="/api-docs">API 文档</router-link>
      </nav>
    </header>
    <main>
      <router-view />
    </main>
  </div>
</template>

<script setup>
// App 组件不需要额外的逻辑
</script>

<style>
#app {
  font-family: Avenir, Helvetica, Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  text-align: center;
  color: #2c3e50;
  margin-top: 60px;
}

header {
  background-color: #2c3e50;
  color: white;
  padding: 20px;
  margin-bottom: 30px;
}

header h1 {
  margin: 0 0 10px 0;
}

nav {
  margin-top: 10px;
}

nav a {
  color: white;
  text-decoration: none;
  margin-right: 20px;
  padding: 5px 10px;
  border-radius: 4px;
}

nav a.router-link-active {
  background-color: #42b883;
}

nav a:hover {
  background-color: #42b883;
}

main {
  padding: 0 20px;
}
</style>