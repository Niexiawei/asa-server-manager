import {defineConfig, loadEnv} from 'vite'
import vue from '@vitejs/plugin-vue'
import jsx from '@vitejs/plugin-vue-jsx'
import {fileURLToPath, URL} from 'node:url'

// https://vitejs.dev/config/

export default ({mode}) => {
    const env = loadEnv(mode, process.cwd());
    return defineConfig({
        plugins: [vue(), jsx()],
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

                        // 趋势图：只有资源监控相关页面用得到，别混进主包
                        if (id.includes('uplot')) {
                            return 'uplot'
                        }
                        // 4️⃣ 其他第三方
                        if (
                            id.includes('dayjs')
                        ) {
                            return 'vendor'
                        }

                        return 'vendor'
                    },
                },
            },
            sourcemap: false,
        },
        css: {
            preprocessorOptions: {
                less: {
                    additionalData: `
                        @import "@/app.less";
                    `
                }
            }
        },
        server: {
            host: '0.0.0.0',
            port: 3000,
            proxy: {
                '/api': {
                    target: env.VITE_PROXY_TARGET,
                    //target: 'https://192.168.2.26:19193',
                    changeOrigin: true,
                    // 后端用的是本地 CA 签发的证书，Node 侧不校验（浏览器侧靠 CA 已进系统存储）
                    secure: false,
                    ws: true,
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
}


