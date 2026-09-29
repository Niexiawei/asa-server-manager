//go:build linux

package userenv

import "os"

// SetProfileScript 在 /etc/profile.d/<file> 写一个 `export name='value'` 片段，
// 返回写入的路径。此后**新的登录 shell**（SSH 登录、`su -`、`sudo -i`、桌面登录）
// 都会带上这个环境变量。需要 root；非 root 返回 ErrNeedRoot。
//
// 它改不了任何已经在运行的进程——包括调用它的那个 shell：子进程无法修改父进程的
// 环境，这是 Unix 的进程模型，没有绕过的办法。当前终端要么重新登录，要么手动
// `source <返回的路径>`。
func SetProfileScript(file, name, value string) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrNeedRoot
	}
	return writeProfileScriptIn(profileDir, file, name, value)
}
