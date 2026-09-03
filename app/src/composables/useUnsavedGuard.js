import {onBeforeRouteLeave} from 'vue-router'
import {DialogPlugin} from 'tdesign-vue-next'

// 常驻编辑区替代抽屉后的「未保存」保护：
// - promptDiscard(dirty)：dirty 为真时弹确认，返回 Promise<boolean>（true = 放弃修改并继续）
// - confirmLeave()：按传入的「是否有任意脏数据」判断，供路由离开 / 整页操作使用
// - 自动注册 onBeforeRouteLeave
export function useUnsavedGuard(isAnyDirty) {
  const promptDiscard = (dirty) =>
      new Promise((resolve) => {
        if (!dirty) {
          resolve(true)
          return
        }
        const d = DialogPlugin.confirm({
          header: '有未保存的修改',
          body: '当前页面存在尚未保存的修改，离开后这些内容将丢失。确定要离开吗？',
          theme: 'warning',
          confirmBtn: {content: '放弃修改并离开', theme: 'warning'},
          cancelBtn: '继续编辑',
          onConfirm: () => {
            d.hide()
            resolve(true)
          },
          onCancel: () => resolve(false),
          onClose: () => resolve(false),
        })
      })

  const confirmLeave = () => promptDiscard(!!isAnyDirty())

  onBeforeRouteLeave(async () => await confirmLeave())

  return {promptDiscard, confirmLeave}
}
