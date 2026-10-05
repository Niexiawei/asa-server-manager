package meshcoord

import (
	"os/exec"
	"strings"
	"testing"
)

// listDeps 返回 pkgs 的完整依赖闭包（含测试之外的全部传递依赖）。
func listDeps(t *testing.T, env []string, pkgs ...string) []string {
	t.Helper()
	if testing.Short() {
		t.Skip("-short：跳过需要 go 命令的依赖守卫")
	}
	cmd := exec.Command("go", append([]string{"list", "-deps"}, pkgs...)...)
	cmd.Dir = "../.."
	cmd.Env = append(cmd.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list 失败（没有 go 工具链？）：%v", err)
	}
	return strings.Fields(string(out))
}

// 协调节点是独立部署的小二进制：不得把 GUI、游戏状态库、frp、Web 框架拖进来（§12 P1-5）。
// 交叉编译到 Linux 看——那才是它的部署形态，也是 GUI 依赖最可能经平台文件混进来的地方。
func TestCoordinatorDependencyGuard(t *testing.T) {
	deps := listDeps(t, []string{"GOOS=linux", "CGO_ENABLED=0"}, "./cmd/asa-coordinator")
	banned := []string{"fyne.io/", "github.com/dgraph-io/badger", "github.com/fatedier/frp", "github.com/gin-gonic/",
		"github.com/Niexiawei/simple-file-sync"}
	for _, d := range deps {
		for _, b := range banned {
			if strings.HasPrefix(d, b) {
				t.Errorf("asa-coordinator 不应依赖 %s（经由 %s）", b, d)
			}
		}
	}
}

// 管理器互控与 simple-file-sync 毫无关系，不 import 它的任何包（§3.1、D0）。
func TestMeshDoesNotImportSimpleFileSync(t *testing.T) {
	deps := listDeps(t, nil, "./internal/mesh/...", "./internal/meshcoord", "./pkg/meshid", "./pkg/meshjoin",
		"./pkg/streamconn", "./pkg/stun", "./cmd/asa-coordinator")
	for _, d := range deps {
		if strings.HasPrefix(d, "github.com/Niexiawei/simple-file-sync") {
			t.Errorf("mesh 相关包不应依赖 simple-file-sync：%s", d)
		}
	}
}
