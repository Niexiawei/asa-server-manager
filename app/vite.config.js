import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import {fileURLToPath, URL} from 'node:url'

// https://vitejs.dev/config/
export default defineConfig({
    plugins: [vue()],
    build: {
        minify: 'terser',
        terserOptions: {
            compress: {
                drop_console: true,
                drop_debugger: true
            }
        },
        rollupOptions: {
            output: {
                manualChunks(id) {
                    if (!id.includes('node_modules')) return

                    // 1️⃣ Vue 核心
                    if (
                        id.includes('/vue/') ||
                        id.includes('vue-router') ||
                        id.includes('@vueuse')
                    ) {
                        return 'vue'
                    }

                    // 2️⃣ Monaco（必须独立）
                    if (id.includes('monaco-editor')) {
                        return 'monaco'
                    }

                    // 3️⃣ Web Terminal（体积不小）
                    if (id.includes('vue-web-terminal')) {
                        return 'terminal'
                    }

                    if (id.includes('axios')) {
                        return 'axios'
                    }

                    if (id.includes('arco-design')) {
                        return 'arco-design'
                    }

                    // 4️⃣ 其他第三方
                    if (
                        id.includes('dayjs') ||
                        id.includes('vue-masonry-wall')
                    ) {
                        return 'vendor'
                    }

                    return 'vendor'
                },
            },
        },
        sourcemap: false,
    },
    server: {
        host: '0.0.0.0',
        port: 3000,
        proxy: {
            '/api': {
                target: 'http://localhost:19193',
                //target: 'http://192.168.2.26:19193',
                changeOrigin: true,
                secure: false,
                rewrite: path => path.replace(/^\/api/, '')
            },
        },
    },
    resolve: {
        alias: {
            "@": fileURLToPath(new URL('./src', import.meta.url)),
        }
    },
})