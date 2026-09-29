//go:build windows

package userenv

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// 真写 HKCU\Environment：用一个带 PID 与时间戳的一次性变量名，结束时删除。
func TestUserRoundTrip(t *testing.T) {
	name := fmt.Sprintf("ASA_USERENV_TEST_%d_%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { _ = UnsetUser(name) })

	if _, ok, err := Get(User, name); err != nil || ok {
		t.Fatalf("写入前应不存在：ok=%v err=%v", ok, err)
	}
	const value = `D:\ASA 配置\cfg`
	if err := SetUser(name, value); err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	if got, ok, err := Get(User, name); err != nil || !ok || got != value {
		t.Fatalf("读回 = %q ok=%v err=%v，期望 %q", got, ok, err, value)
	}
	// 系统级不受影响。
	if _, ok, err := Get(Machine, name); err != nil || ok {
		t.Errorf("系统级不应出现该变量：ok=%v err=%v", ok, err)
	}
	if err := UnsetUser(name); err != nil {
		t.Fatalf("UnsetUser: %v", err)
	}
	if _, ok, _ := Get(User, name); ok {
		t.Error("删除后应不存在")
	}
	if err := UnsetUser(name); err != nil {
		t.Errorf("重复删除不应报错: %v", err)
	}
}

func TestGetMachineReadable(t *testing.T) {
	// 系统级 Path 在任何 Windows 上都存在；普通用户对该键有读权限。
	if _, ok, err := Get(Machine, "Path"); err != nil || !ok {
		t.Errorf("应能读到系统级 Path：ok=%v err=%v", ok, err)
	}
}
