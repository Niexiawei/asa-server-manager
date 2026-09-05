//go:build linux

package vcredist

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"asa-server/pkg/download"
	"asa-server/pkg/fsutil"
	"asa-server/pkg/umu"
)

// 本文件是「把微软 VC++ 运行时装进一个 Wine 前缀」的编排：下载安装包、写 DLL
// override、跑安装器、写标记。判据在 vcredist.go，只读诊断在 inspect.go。
//
// 它**不认识任何 ASA 概念**，也不生成任何面向本程序用户的指引文案：凡是要提到
// 「asa-server setup」「linux.install_vcredist」这类本程序自己的名字的地方，都做成
// 类型化的结果（Result.Skip / AutoDownloadDisabledError / OnUnverifiedDownload），
// 由调用方翻成人话。见 docs/RUNNER_INSTANCE_PACKAGE_SPLIT_TODO.md §6。
//
// 带 build tag 的只有本文件：它依赖 pkg/umu（在 prefix 里跑一个 exe），而 umu/Wine/
// Proton 没有 Windows 对应物。vcredist.go 与 inspect.go 仍然无 tag、全平台可单测。

// installTimeout 给安装器的硬上限。/quiet 理论上不弹窗，但 Wine 下的 Burn
// 引导器仍可能弹出一个没人点的对话框，从而永久挂住整个安装流程。
const installTimeout = 15 * time.Minute

// overrideImportTimeout 给 regedit 导入的上限。它只是导一个 .reg，几秒钟的事。
const overrideImportTimeout = time.Minute

// Config 是 Installer 需要的一切。三个回调都可以是 nil（各自退化成合理的默认）。
type Config struct {
	// Dir 是安装包与生成的 .reg 的落地目录。
	Dir string
	// URL 空 = DefaultURL。最终下载地址的路径里自带文件 sha256，会被自动提取用于
	// 校验；自建镜像若没有那一段，用 SHA256 显式指定。
	URL    string
	SHA256 string
	// AutoDownload 为 false 时，本地没有安装包就返回 *AutoDownloadDisabledError，
	// 由调用方翻成「怎么手动放一份进去」的指引 —— 那句话里全是本程序自己的名字。
	AutoDownload bool

	// Umu 是「在 prefix 里跑一个 Windows exe」的能力来源。必填。
	Umu *umu.Runtime

	// AcquireDisplay 拿一个能用的 X 显示。安装器（不是 override）需要它 ——
	// WiX Burn 引导器即使带 /quiet 也要初始化 UI 子系统，连不上 X 就以
	// ExitNoDisplay 退出。
	//
	// 返回的 error 若 errors.Is(err, ErrNoDisplay)，表示这台机器**压根没有**显示
	// 能力（常规路径，不是意外）；其它 error 表示有能力但这次没拿到（Xvfb 起不来）。
	// 两者都只跳过安装、不算失败，但调用方给用户的指引不同，所以必须分得开。
	//
	// nil = 当作没有显示能力。
	AcquireDisplay func() (env []string, how string, err error)

	// ChownPath 把一个路径交给运行时用户。prefix 归它，别在里面留 root 属主的
	// 文件。nil = 不做（没有降权时的正常情况）。
	ChownPath func(string) error

	// OnUnverifiedDownload 在**下载开始前**通知调用方「这一次没有校验值可用」。
	//
	// 做成钩子而不是 Result 里的一个字段，是因为顺序有意义：事后再说，用户已经
	// 无校验地下完 24 MiB 了。也正因为这条提醒的后半句（「可以用哪个配置项显式
	// 指定」）全是调用方自己的名字，它不能由本包来写。
	OnUnverifiedDownload func(url string)
}

// ErrNoDisplay 是 Config.AcquireDisplay 用来表达「这台机器压根没有可用显示」的
// 哨兵：调用方把自己那套「为什么没有」的判断包成 fmt.Errorf("%w: %s", ErrNoDisplay,
// reason) 返回，本包只需要区分它与「有显示能力但这次没拿到」。
var ErrNoDisplay = errors.New("no usable X display on this host")

// AutoDownloadDisabledError 表示本地没有安装包、而自动下载被关掉了。
//
// 是类型而不是一句话：调用方要用 Dest/URL 拼一句带自己配置项名字的指引
// （「请手动下载 <URL> 放到 <Dest>，或把某某开关关掉」），那句话里没有一个词
// 是本包该知道的。
type AutoDownloadDisabledError struct {
	Dest string // 安装包该放的位置
	URL  string // 该从哪下
}

func (e *AutoDownloadDisabledError) Error() string {
	return fmt.Sprintf("vcredist: auto-download is off and %s is missing (source: %s)", e.Dest, e.URL)
}

// SkipReason 说明第二步（把运行时装进 system32）为什么没做。
//
// 两种都**不是失败** —— Ensure 返回 nil error，第一步的 DLL override 已经写好，
// 普通实例不受影响。但它们的成因不同，调用方给出的指引也不同，所以分成两个值
// 而不是一个 bool。
type SkipReason string

const (
	SkipNone SkipReason = ""
	// SkipNoDisplay：这台机器压根没有显示能力。常规路径 —— 一台没装 Xvfb 的
	// 无头机会一路走到这里。
	SkipNoDisplay SkipReason = "no-display"
	// SkipDisplayUnavailable：有显示能力，但这一次没拿到（Xvfb 起不来等）。
	SkipDisplayUnavailable SkipReason = "display-unavailable"
	// SkipAlreadyInstalled：system32 里本来就是微软原生的，无事可做。
	SkipAlreadyInstalled SkipReason = "already-installed"
)

// Result 是 Ensure 的结构化结果。
//
// 它存在的理由：「VC++ 没装成，因为这台机器没有显示」这个事实以前只活在一行日志
// 文本里，诊断接口拿不到。做成结果之后它是可检视的。
type Result struct {
	// OverridesApplied：第一步（DLL override）做成了。承重的是这一环。
	OverridesApplied bool
	// Installed：第二步（安装进 system32）之后，探针 DLL 是微软原生的。
	Installed bool
	// Skip 说明第二步为什么没做；SkipNone = 做了。
	Skip SkipReason
	// SkipCause 是 Skip 的原文（拿不到显示时的具体错误）。
	SkipCause error
}

// Installer 编排一次 VC++ 运行时安装。
//
// 不持有任何跨调用状态，所以调用方每次现 New 一个即可，不必做成单例
// （同 pkg/sysuser.Manager；与 pkg/umu.Runtime、pkg/xvfb.Manager 那种持有活进程的
// 相反，那两个必须 Reconfigure 而不是重新 New）。
type Installer struct{ cfg Config }

// New returns an Installer for cfg.
func New(cfg Config) *Installer { return &Installer{cfg: cfg} }

func (i *Installer) dir() string     { return i.cfg.Dir }
func (i *Installer) exePath() string { return filepath.Join(i.dir(), InstallerName) }
func (i *Installer) regPath() string { return filepath.Join(i.dir(), OverrideRegName) }
func (i *Installer) chown(p string) error {
	if i.cfg.ChownPath == nil {
		return nil
	}
	return i.cfg.ChownPath(p)
}

// Ensure 把微软 VC++ 运行时装进 prefix。幂等：已装好时只做几次本地文件读取就返回。
//
// 分两步，且**顺序不能颠倒**（与 winetricks 的 load_vcrun2022 一致）：
//
//	①DLL override —— 承重的那一环，无条件执行。
//	  真机实测发现 ARK 服务端**自己就在 exe 同目录带了 11 个 VC++ 运行时 DLL 里的
//	  9 个原生版**（vcruntime140 / msvcp140 / concrt140 …）。Windows 的 DLL 搜索
//	  顺序里应用目录优先于 system32，所以那些原生 DLL 本来就在正确的位置上 ——
//	  唯一挡路的是 Wine 默认优先加载自己的内建实现。override 就是掀开这道门的开关。
//	  它无头可用、零依赖、几秒钟跑完。
//
//	②把运行时也装进 system32 —— 补充项，不是必需项。
//	  多补的是游戏没自带的那两个（vcomp140 / vcamp140），以及任何不经应用目录解析
//	  的加载路径。**装不了不是失败**：它需要一个能连上的 X 显示，无头机上一律
//	  ExitNoDisplay，那种情况下返回 Skip 而不是 error。
func (i *Installer) Ensure(ctx context.Context, prefix string, logf func(string, ...any)) (Result, error) {
	var res Result

	if i.cfg.Umu == nil {
		return res, errors.New("vcredist: Config.Umu is required")
	}
	if !umu.PrefixInitialized(prefix) {
		return res, fmt.Errorf("Wine 前缀 %s 尚未初始化，无法安装 VC++ 运行时", prefix)
	}

	if err := i.applyOverrides(ctx, prefix, logf); err != nil {
		return res, fmt.Errorf("写入 VC++ DLL override 失败: %w", err)
	}
	res.OverridesApplied = true

	if InstalledIn(prefix) {
		res.Installed, res.Skip = true, SkipAlreadyInstalled
		return res, nil
	}

	// 与调用方的启动路径共用同一个显示解析：两者需要显示的原因是同一个
	// （Wine 的 winex11.drv），分成两套判断只会让它们慢慢漂开。
	displayEnv, how, err := i.acquireDisplay()
	if err != nil {
		res.Skip, res.SkipCause = classifySkip(err), err
		return res, nil
	}
	logf("VC++ 运行时安装将使用 %s", how)

	exePath, checksum, err := i.ensureInstaller(ctx, logf)
	if err != nil {
		return res, err
	}

	logf("正在把 VC++ 运行时装进 Wine 前缀 %s（可能需要几分钟）...", prefix)
	tail, runErr := i.cfg.Umu.RunInPrefix(ctx, prefix,
		[]string{exePath, "/install", "/quiet", "/norestart"},
		runOptions(installTimeout, displayEnv), logf)
	if errors.Is(runErr, umu.ErrNoInterpreter) {
		return res, runErr
	}
	code := umu.ExitCode(runErr)

	umu.WaitForWineserverDrain(prefix)

	// 后置条件才是判决。退出码只进错误文本 —— 见 ExitNote 的注释。
	if !InstalledIn(prefix) {
		return res, fmt.Errorf("VC++ 运行时安装后校验未通过：%s 仍是 Wine 自带的（DOS stub 里还有 Wine 标记）。"+
			"安装器%s，最后的输出：\n%s",
			System32(prefix, ProbeDLL), ExitNote(code), tail)
	}
	res.Installed = true
	if !ExitOK(code) {
		logf("注意：VC++ 运行时校验已通过，但%s", ExitNote(code))
	}

	if err := i.writeMarker(prefix, exePath, checksum); err != nil {
		logf("警告：写 VC++ 安装标记失败（%v），不影响使用", err)
	}
	logf("VC++ 运行时已装入 Wine 前缀")
	return res, nil
}

// classifySkip 把 AcquireDisplay 的失败分成两档。
//
// 两者都只跳过安装、都不算失败，但调用方给用户的指引不同：
// 「本机没有显示能力」该去装一个 X 服务，「有能力但这次没拿到」该去看
// 失败原文（常见是 Xvfb 起不来）。分不开就只能给一句和稀泥的提示。
func classifySkip(err error) SkipReason {
	if errors.Is(err, ErrNoDisplay) {
		return SkipNoDisplay
	}
	return SkipDisplayUnavailable
}

// acquireDisplay 调回调，没配回调就等同于「本机没有显示能力」。
func (i *Installer) acquireDisplay() ([]string, string, error) {
	if i.cfg.AcquireDisplay == nil {
		return nil, "", ErrNoDisplay
	}
	return i.cfg.AcquireDisplay()
}

// --- 安装包 -----------------------------------------------------------------

// ensureInstaller 保证安装包在本地，返回其路径与实际使用的校验值
// （校验值仅用于写进标记文件，便于日后复现）。
func (i *Installer) ensureInstaller(ctx context.Context, logf func(string, ...any)) (string, string, error) {
	dest := i.exePath()
	if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
		logf("使用已下载的 VC++ 运行时安装包：%s", dest)
		return dest, "", i.makeReadable(dest)
	}

	srcURL := i.cfg.URL
	if srcURL == "" {
		srcURL = DefaultURL
	}
	if !i.cfg.AutoDownload {
		return "", "", &AutoDownloadDisabledError{Dest: dest, URL: srcURL}
	}

	// 跟随重定向拿最终地址：微软的最终 URL 路径里自带文件 sha256。
	finalURL, err := download.ResolveFinalURL(ctx, srcURL)
	if err != nil {
		logf("解析 %s 的最终下载地址失败（%v），按原地址下载", srcURL, err)
		finalURL = srcURL
	}

	checksum := ""
	switch {
	case i.cfg.SHA256 != "":
		checksum = "sha256:" + strings.ToLower(strings.TrimSpace(i.cfg.SHA256))
	default:
		if h, ok := SHA256FromDownloadURL(finalURL); ok {
			checksum = "sha256:" + h
		} else if i.cfg.OnUnverifiedDownload != nil {
			// 下载**之前**通知，理由见 Config.OnUnverifiedDownload。
			i.cfg.OnUnverifiedDownload(finalURL)
		}
	}

	logf("正在下载 VC++ 运行时安装包（约 24 MiB）：%s", finalURL)
	if err := download.Fetch(ctx, download.Options{
		URL:      finalURL,
		Dest:     dest,
		Checksum: checksum,
		Resume:   true,
		Progress: download.ProgressLogger("vc_redist", logf),
	}); err != nil {
		return "", "", fmt.Errorf("下载 VC++ 运行时安装包失败: %w", err)
	}
	return dest, checksum, i.makeReadable(dest)
}

// makeReadable 让降权后的运行时用户能读到安装包。
//
// 用 chmod 而不是 chown：这是个只读产物，降权用户只需要读+穿过目录，不需要属主
// —— 与 fsutil.EnsureWorldReadable 对 proton/umu-launcher 两个只读子树的处理同类。
func (i *Installer) makeReadable(dest string) error {
	if err := os.Chmod(i.dir(), 0o755); err != nil {
		return err
	}
	return os.Chmod(dest, 0o755)
}

// --- DLL override -------------------------------------------------------------

// applyOverrides 生成 .reg 并用 prefix 自带的 regedit.exe 静默导入。
//
// **故意不带显示**：这一步无头可用，也正因如此它才是承重的那一环。
//
// winetricks 在 wow64 下会跑 32 位与 64 位两遍（只跑一遍时只进 32 位视图）；
// 我们经 umu-run 跑的是 GE-Proton 的 64 位 wine，正是需要的那一遍 ——
// ArkApi 与 ASA 服务端都是纯 x64，不需要 32 位视图。
func (i *Installer) applyOverrides(ctx context.Context, prefix string, logf func(string, ...any)) error {
	regPath := i.regPath()
	if err := os.MkdirAll(filepath.Dir(regPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(regPath, []byte(BuildOverrideReg()), 0o644); err != nil {
		return err
	}

	regedit, err := prefixRegedit(prefix)
	if err != nil {
		return err
	}

	logf("正在写入 %d 条 VC++ DLL override（%s）", len(OverrideDLLs), OverrideMode)
	// regedit.exe 传宿主路径（umu 对普通 exe 走 resolve(strict=True)）；
	// 它的**参数**是 Windows 路径，所以过 umu.GamePath。
	tail, err := i.cfg.Umu.RunInPrefix(ctx, prefix,
		[]string{regedit, "/S", umu.GamePath(regPath)},
		runOptions(overrideImportTimeout, nil), logf)
	if errors.Is(err, umu.ErrNoInterpreter) {
		return err
	}
	if err != nil {
		return fmt.Errorf("regedit 导入失败%s：\n%s", ExitNote(umu.ExitCode(err)), tail)
	}
	return nil
}

// prefixRegedit 定位 prefix 里的 64 位 regedit.exe。Wine 装在 C:\windows\regedit.exe
// （winetricks 的 w_try_regedit64 用的也是这个路径）；system32 下的那份是兜底。
func prefixRegedit(prefix string) (string, error) {
	candidates := []string{
		filepath.Join(prefix, "drive_c", "windows", "regedit.exe"),
		filepath.Join(prefix, "drive_c", "windows", "system32", "regedit.exe"),
	}
	for _, p := range candidates {
		if fsutil.FileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("Wine 前缀 %s 里找不到 regedit.exe", prefix)
}

// runOptions 是本包两次 umu-run 调用共用的选项。
//
// 与 umu.WarmPrefix（wineboot）逐项对齐，只有三处刻意的不同：
//   - NoRuntimeUpdate：运行时这时早就装好了，没有理由让 umu 再去
//     repo.steampowered.com 查更新（WarmPrefix 那一次故意不带，因为它必须能拉运行时）。
//   - Verb "run"：不能用 umu 默认的 waitforexitandrun。这里尤其致命 —— 共享 prefix 上
//     只要有实例在跑，`wineserver -w` 就永不返回，整条调用会一路挂到硬超时才报错，
//     且错得毫无线索。
//   - 有硬超时：见 installTimeout。
//
// 显示以 ExtraEnv 进来（追加在最后，理由见 umu.RunOptions.ExtraEnv）。
func runOptions(timeout time.Duration, displayEnv []string) umu.RunOptions {
	return umu.RunOptions{
		Timeout:         timeout,
		ExtraEnv:        displayEnv,
		NoRuntimeUpdate: true,
		Verb:            "run",
	}
}

// --- 标记 ---------------------------------------------------------------------

// writeMarker 在 prefix **内部**记一笔装的是哪个包 —— 放在里面，是为了让「换了
// 一代 prefix」自动作废这个标记，不需要额外的失效逻辑（见 MarkerFileName）。
func (i *Installer) writeMarker(prefix, exePath, checksum string) error {
	body := fmt.Sprintf("installer=%s\nchecksum=%s\ninstalled_at=%s\n",
		exePath, checksum, time.Now().Format(time.RFC3339))
	path := filepath.Join(prefix, MarkerFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	// prefix 归运行时用户，别在里面留 root 属主的文件。
	return i.chown(path)
}
