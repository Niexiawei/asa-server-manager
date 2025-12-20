import {defineConfig} from 'vite'
import vue from '@vitejs/plugin-vue'
import {fileURLToPath, URL} from 'node:url'
import terser from '@rollup/plugin-terser'

// https://vitejs.dev/config/
export default defineConfig({
    plugins: [vue()],
    build: {
        minify: 'terser',
        terserOptions: {
            compress: {
                drop_console: true
            }
        }
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