//go:build windows

package folderpicker

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 直接走 vtable 调 COM，不引入 go-ole：只需要这一个对话框，四五个方法。
// vtable 下标按 ShObjIdl_core.h 的声明顺序（IUnknown 3 个 → IModalWindow 1 个 →
// IFileDialog 23 个 → IFileOpenDialog 2 个）。

var (
	clsidFileOpenDialog = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidIFileOpenDialog  = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidIShellItem       = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

const (
	vtRelease = 2
	// IModalWindow
	vtShow = 3
	// IFileDialog
	vtSetOptions = 9
	vtGetOptions = 10
	vtSetFolder  = 12
	vtSetTitle   = 17
	vtGetResult  = 20
	// IShellItem
	vtGetDisplayName = 5

	fosNoChangeDir      = 0x00000008
	fosPickFolders      = 0x00000020
	fosForceFileSystem  = 0x00000040
	fosPathMustExist    = 0x00000800
	sigdnFileSysPath    = 0x80058000
	clsctxInprocServer  = 0x1
	coinitApartment     = 0x2
	coinitDisableOle1DD = 0x4

	hrOK          = 0
	hrFalse       = 1
	hrCancelled   = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
	hrChangedMode = 0x80010106 // RPC_E_CHANGED_MODE
)

var (
	ole32                           = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree               = ole32.NewProc("CoTaskMemFree")
	shell32                         = windows.NewLazySystemDLL("shell32.dll")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	user32                          = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow         = user32.NewProc("GetForegroundWindow")
)

// comObject 是任意 COM 接口指针指向的内存布局：第一个字段是 vtable 指针。
// vtable 声明成足够大的数组，只按真实存在的下标访问。
type comObject struct {
	vtbl *[32]uintptr
}

func (o *comObject) call(index int, args ...uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(o.vtbl[index], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return hr
}

func (o *comObject) release() { o.call(vtRelease) }

func uintptrOf(p *uint32) uintptr       { return uintptr(unsafe.Pointer(p)) }
func uintptrOfCOM(o *comObject) uintptr { return uintptr(unsafe.Pointer(o)) }

func hrError(op string, hr uintptr) error {
	return fmt.Errorf("folderpicker: %s 失败 (HRESULT 0x%08X)", op, uint32(hr))
}

// ForegroundWindow 返回当前前台窗口的句柄，给 Pick 当 owner：Fyne 不暴露自己窗口
// 的 HWND，但用户点「浏览…」的那一刻前台必然是那个窗口。
func ForegroundWindow() uintptr {
	h, _, _ := procGetForegroundWindow.Call()
	return h
}

// Pick 弹出原生「选择文件夹」对话框，阻塞到用户选定或取消。
//
//   - 选定：path 为文件系统路径，ok=true；
//   - 取消：ok=false、err=nil；
//   - COM 初始化 / 创建失败：err 非空，调用方应回退到别的选择器。
//
// initialDir 为空或不存在时由系统决定起始位置；owner 为 0 时对话框无主窗口。
//
// 线程：Show 是模态阻塞调用，且要求 STA。本函数自己 LockOSThread 并在该线程上
// CoInitializeEx(STA)。**不要在 GUI 框架的主线程 / 事件循环里调用**——Show 期间
// 那个线程被占住，框架的渲染循环会停摆。应该另起 goroutine 调用，结果再投递回 UI 线程。
func Pick(title, initialDir string, owner uintptr) (path string, ok bool, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartment|coinitDisableOle1DD)
	switch hr {
	case hrOK, hrFalse:
		defer procCoUninitialize.Call()
	case hrChangedMode:
		// 这个 OS 线程先前已被初始化成 MTA（Go 的线程会复用）；换不成 STA，交给调用方回退。
		return "", false, hrError("CoInitializeEx(STA)", hr)
	default:
		return "", false, hrError("CoInitializeEx", hr)
	}

	dlg, err := newFolderDialog()
	if err != nil {
		return "", false, err
	}
	defer dlg.release()

	if title != "" {
		if t, err := syscall.UTF16PtrFromString(title); err == nil {
			dlg.call(vtSetTitle, uintptr(unsafe.Pointer(t)))
		}
	}
	if initialDir != "" {
		if item, err := shellItemFromPath(initialDir); err == nil {
			dlg.call(vtSetFolder, uintptr(unsafe.Pointer(item)))
			item.release()
		}
	}

	switch hr := dlg.call(vtShow, owner); hr {
	case hrOK:
	case hrCancelled:
		return "", false, nil
	default:
		return "", false, hrError("IFileDialog::Show", hr)
	}

	var item *comObject
	if hr := dlg.call(vtGetResult, uintptr(unsafe.Pointer(&item))); hr != hrOK || item == nil {
		return "", false, hrError("IFileDialog::GetResult", hr)
	}
	defer item.release()

	var p *uint16
	if hr := item.call(vtGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))); hr != hrOK || p == nil {
		// FOS_FORCEFILESYSTEM 下正常不会出现：选到了没有文件系统路径的虚拟位置。
		return "", false, hrError("IShellItem::GetDisplayName", hr)
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return windows.UTF16PtrToString(p), true, nil
}

// newFolderDialog 创建 IFileOpenDialog 并设成「选文件夹、只要文件系统路径」。
// 调用方必须已在当前线程 CoInitializeEx。
func newFolderDialog() (*comObject, error) {
	var dlg *comObject
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)), uintptr(unsafe.Pointer(&dlg)))
	if hr != hrOK || dlg == nil {
		return nil, hrError("CoCreateInstance(FileOpenDialog)", hr)
	}
	var opts uint32
	if hr := dlg.call(vtGetOptions, uintptr(unsafe.Pointer(&opts))); hr != hrOK {
		dlg.release()
		return nil, hrError("IFileDialog::GetOptions", hr)
	}
	opts |= fosPickFolders | fosForceFileSystem | fosPathMustExist | fosNoChangeDir
	if hr := dlg.call(vtSetOptions, uintptr(opts)); hr != hrOK {
		dlg.release()
		return nil, hrError("IFileDialog::SetOptions", hr)
	}
	return dlg, nil
}

func shellItemFromPath(path string) (*comObject, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var item *comObject
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(p)), 0,
		uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&item)))
	if hr != hrOK || item == nil {
		return nil, hrError("SHCreateItemFromParsingName", hr)
	}
	return item, nil
}
