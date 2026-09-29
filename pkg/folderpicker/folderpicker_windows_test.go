//go:build windows

package folderpicker

import (
	"runtime"
	"testing"
)

// Show 是模态的，单测跑不了；这里走一遍 Show 之前的全部 COM 调用（创建对话框、
// 设选项、设标题、按路径建 IShellItem 并 SetFolder），确认 GUID、vtable 下标与
// 调用约定都对——下标错一位通常就是当场崩溃或 E_NOTIMPL。
func TestDialogSetupWithoutShow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartment|coinitDisableOle1DD)
	switch hr {
	case hrOK, hrFalse:
		defer procCoUninitialize.Call()
	default:
		t.Skipf("当前测试线程无法初始化为 STA (HRESULT 0x%08X)", uint32(hr))
	}

	dlg, err := newFolderDialog()
	if err != nil {
		t.Fatalf("newFolderDialog: %v", err)
	}
	defer dlg.release()

	var opts uint32
	if hr := dlg.call(vtGetOptions, uintptrOf(&opts)); hr != hrOK {
		t.Fatalf("GetOptions HRESULT 0x%08X", uint32(hr))
	}
	if opts&fosPickFolders == 0 || opts&fosForceFileSystem == 0 {
		t.Errorf("选项未生效：0x%08X", opts)
	}

	item, err := shellItemFromPath(t.TempDir())
	if err != nil {
		t.Fatalf("shellItemFromPath: %v", err)
	}
	defer item.release()
	if hr := dlg.call(vtSetFolder, uintptrOfCOM(item)); hr != hrOK {
		t.Errorf("SetFolder HRESULT 0x%08X", uint32(hr))
	}

	if _, err := shellItemFromPath(`Z:\definitely\not\here\` + t.Name()); err == nil {
		t.Error("不存在的路径应建不出 IShellItem")
	}
}
