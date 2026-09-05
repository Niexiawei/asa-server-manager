//go:build linux

package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"asa-server/pkg/download"
	"asa-server/pkg/xvfb"
)

// vcRedistInstallTimeout 给安装器的硬上限。/quiet 理论上不弹窗，但 Wine 下的 Burn
// 引导器仍可能弹出一个没人点的对话框，从而永久挂住 setup。
const vcRedistInstallTimeout = 15 * time.Minute

func vcRedistDir(cfg Config) string { return filepath.Join(cfg.BaseDir, "vcredist") }

func vcRedistExePath(cfg Config) string {
	return filepath.Join(vcRedistDir(cfg), vcRedistInstallerName)
}

func vcRedistOverrideRegPath(cfg Config) string {
	return filepath.Join(vcRedistDir(cfg), vcRedistOverrideRegName)
}

// ensurePrefixVCRedist 是 runner.EnsurePrefixVCRedist 的实现。
func ensurePrefixVCRedist(ctx context.Context, prefixKey string, progress io.Writer) error {
	return ensureVCRedist(ctx, getConfig(), prefixKey, progressLogger(progress))
}

// ensureVCRedist 把微软 VC++ 运行时装进指定 prefix。幂等：已装好时只做几次本地
// 文件读取就返回。
func ensureVCRedist(ctx context.Context, cfg Config, prefixKey string, logf func(string, ...any)) error {
	if !cfg.InstallVCRedist {
		return nil
	}
	// custom 运行时的 prefix 是用户自己搭的，不归我们改。
	if cfg.Runtime != "umu" {
		return nil
	}

	prefix := prefixDir(cfg, prefixKey)
	if !prefixInitialized(prefix) {
		return fmt.Errorf("Wine 前缀 %s 尚未初始化，无法安装 VC++ 运行时", prefix)
	}
	// ---- 第一步：DLL override。这才是承重的那一环 ----
	//
	// 真机实测发现 ARK 服务端**自己就在 exe 同目录带了 11 个 VC++ 运行时 DLL 里的 9 个
	// 原生版**（vcruntime140 / msvcp140 / concrt140 …）。Windows 的 DLL 搜索顺序里应用
	// 目录优先于 system32，所以那些原生 DLL 本来就在正确的位置上 —— 唯一挡路的是
	// Wine 默认优先加载自己的内建实现。override 就是掀开这道门的开关。
	//
	// 它无头可用、零依赖、几秒钟跑完，所以无条件执行，且**先于**安装器
	// （顺序也与 winetricks 的 load_vcrun2022 一致）。
	if err := applyVCRedistOverrides(ctx, cfg, prefix, logf); err != nil {
		return fmt.Errorf("写入 VC++ DLL override 失败: %w", err)
	}

	// ---- 第二步：把运行时也装进 system32。补充项，不是必需项 ----
	//
	// 它多补的是游戏没自带的那两个（vcomp140 / vcamp140），以及任何不经应用目录
	// 解析的加载路径。装不了不是失败。
	if prefixHasVCRedistCfg(cfg, prefixKey) {
		return nil
	}

	// 与 ArkApi 启动路径共用 acquireDisplay：两者需要显示的原因是同一个
	// （Wine 的 winex11.drv），见 display_linux.go。
	display, blocked, dispErr := acquireDisplay(cfg)
	if dispErr != nil {
		// 有显示能力但这次没拿到（Xvfb 起不来）。与下面「本机没有显示」同样只跳过
		// 安装、不阻断 setup，但原因不同，要如实说。
		logf("跳过 VC++ 运行时安装：拿不到图形显示。%v", dispErr)
		logf("  override 已经写好，普通实例不受影响；但 **ArkApi 实例同样起不来**")
		return nil
	}
	if blocked != "" {
		// 缺显示在 preflight 里只是**建议项**（缺它只影响 ArkApi，见 checkDisplay），
		// 所以一台没装 Xvfb 的机器会一路走到这里 —— 这条分支是常规路径，不是意外。
		// 不阻断 setup，但要说清楚代价：ArkApi 在这台机器上同样起不来
		// （不是只有 system32 没补齐）。
		logf("跳过 VC++ 运行时安装：%s。", blocked)
		logf("  override 已经写好，普通实例不受影响；但 **ArkApi 实例同样起不来** ——")
		logf("  AsaApiLoader.exe 也要求有图形显示。请%s，然后重跑 asa-server setup。", xvfb.InstallHint)
		return nil
	}

	exePath, checksum, err := ensureVCRedistInstaller(ctx, cfg, logf)
	if err != nil {
		return err
	}

	logf("正在把 VC++ 运行时装进 Wine 前缀 %s（可能需要几分钟）...", prefix)
	tail, runErr := runInPrefix(ctx, cfg, prefix, exePath,
		[]string{"/install", "/quiet", "/norestart"}, vcRedistInstallTimeout, logf, display)
	code := exitCodeOf(runErr)

	waitForWineserverDrain(prefix)

	// 后置条件才是判决。退出码只进错误文本 —— 见 msInstallerExitNote 的注释。
	if !prefixHasVCRedistCfg(cfg, prefixKey) {
		return fmt.Errorf("VC++ 运行时安装后校验未通过：%s 仍是 Wine 自带的（DOS stub 里还有 Wine 标记）。"+
			"安装器%s，最后的输出：\n%s",
			prefixSystem32(cfg, prefixKey, nativeProbeDLL), msInstallerExitNote(code), tail)
	}
	if !msInstallerExitOK(code) {
		logf("注意：VC++ 运行时校验已通过，但%s", msInstallerExitNote(code))
	}

	if err := writeVCRedistMarker(prefix, exePath, checksum); err != nil {
		logf("警告：写 VC++ 安装标记失败（%v），不影响使用", err)
	}
	logf("VC++ 运行时已装入 Wine 前缀")
	return nil
}

// prefixHasVCRedist 只读判断某个 prefix 里有没有微软原生 VC++ 运行时。
// 不联网、不改动，可以放心在实例启动这种热路径上调。
func prefixHasVCRedist(prefixKey string) bool {
	return prefixHasVCRedistCfg(getConfig(), prefixKey)
}

// prefixHasVCRedistCfg 是显式带 cfg 的版本：安装流程全程用同一份 cfg，免得中途
// Configure 换了指针导致「装到 A 前缀、校验 B 前缀」。
//
// 判据只有一条：system32 下的探针 DLL 的 DOS stub 里没有 Wine 标记。
// 注册表那个「标准检测键」**不能用** —— GE-Proton 在全新 prefix 里就把它伪造好了，
// 见 vcRuntimeRegSection 的注释。
func prefixHasVCRedistCfg(cfg Config, prefixKey string) bool {
	return nativeDLLPresent(prefixSystem32(cfg, prefixKey, nativeProbeDLL))
}

// prefixSystem32 拼出 prefix 里 64 位 system32 下某个文件的路径。
// （win64 prefix 下 system32 是 64 位、syswow64 才是 32 位，别记反。）
func prefixSystem32(cfg Config, prefixKey, name string) string {
	return filepath.Join(prefixDir(cfg, prefixKey), "drive_c", "windows", "system32", name)
}

// nativeDLLPresent 报告 path 是不是一个微软原生 DLL（而非 Wine 的占位/内建 PE）。
func nativeDLLPresent(path string) bool {
	return classifyDLL(path) == DLLNative
}

// classifyDLL 判定一个 DLL 文件的出身。
func classifyDLL(path string) DLLOrigin {
	f, err := os.Open(path)
	if err != nil {
		return DLLMissing
	}
	defer f.Close()

	buf := make([]byte, wineDLLHeaderScan)
	n, err := io.ReadFull(f, buf)
	// 文件比 wineDLLHeaderScan 短时 ReadFull 返回 ErrUnexpectedEOF，但 n 是有效的。
	if n == 0 && err != nil {
		return DLLMissing
	}
	if isWineOwnDLL(buf[:n]) {
		return DLLWine
	}
	return DLLNative
}

// --- 诊断 ---------------------------------------------------------------------

// vcRedistStatus 汇总 prefix 的 VC++ 运行时现状，供 `asa-server verify-arkapi` 展示。
// 只读，不联网。gameDir 传游戏 exe 所在目录（可为空则跳过那一列）。
func vcRedistStatus(prefixKey, gameDir string) VCRedistInfo {
	cfg := getConfig()
	info := VCRedistInfo{
		Managed: cfg.Runtime == "umu",
		Prefix:  prefixDir(cfg, prefixKey),
	}
	info.Installed = prefixHasVCRedistCfg(cfg, prefixKey)
	info.ProbeDLL = nativeProbeDLL

	if data, err := os.ReadFile(filepath.Join(info.Prefix, "system.reg")); err == nil {
		info.RegistryVersion = vcRuntimeRegistryVersion(data)
	}
	if data, err := os.ReadFile(filepath.Join(info.Prefix, "user.reg")); err == nil {
		info.OverridesSet = countVCRedistOverrides(data)
	}
	info.WantOverrides = len(vcRedistOverrideDLLs)

	// 诊断视图，只问计划不动手 —— `verify-arkapi --check-only` 不该顺手起个 X 服务。
	// 报候选链的头一档：安装真跑起来时先试的就是它。
	if plans, blocked := planDisplay(cfg); blocked != "" {
		info.InstallerBlocked = blocked
	} else {
		info.InstallerDisplay = plans[0].How
	}

	for _, name := range vcRedistOverrideDLLs {
		d := VCRedistDLLInfo{Name: name + ".dll"}
		d.InSystem32 = classifyDLL(prefixSystem32(cfg, prefixKey, d.Name))
		if gameDir != "" {
			d.InGameDir = classifyDLL(filepath.Join(gameDir, d.Name))
		}
		info.DLLs = append(info.DLLs, d)
	}
	return info
}

// prefixHasVCRedistOverrides 报告某个 prefix 的 user.reg 里我们那批 DLL override
// 是否已经齐了。**只读一个文件**，所以可以放在实例启动这种热路径上。
//
// 它与 prefixHasVCRedist 判的不是同一件事，别混用：后者看 system32 里有没有微软
// 原生 DLL（= vc_redist 安装器跑成功过），而安装器在没有图形显示的机器上**永远
// 装不上**（退出码 203）。拿它当「要不要再试一次」的判据，会让无头机每次启动都
// 重跑一遍 ensureVCRedist —— 那里面有一次 regedit 容器启动，好几秒。
// override 才是承重的那一环，也是无头可用的那一环，所以由它当判据。
func prefixHasVCRedistOverrides(prefix string) bool {
	data, err := os.ReadFile(filepath.Join(prefix, "user.reg"))
	if err != nil {
		return false
	}
	return countVCRedistOverrides(data) >= len(vcRedistOverrideDLLs)
}

// countVCRedistOverrides 数 user.reg 的 DllOverrides 段里有几条是我们写的
// "*<dll>"="native,builtin"。
func countVCRedistOverrides(userReg []byte) int {
	text := string(userReg)
	n := 0
	for _, dll := range vcRedistOverrideDLLs {
		if strings.Contains(text, fmt.Sprintf("\"*%s\"=\"%s\"", dll, vcRedistOverrideMode)) {
			n++
		}
	}
	return n
}

// --- 安装包 -----------------------------------------------------------------

// ensureVCRedistInstaller 保证安装包在本地，返回其路径与实际使用的校验值
// （校验值仅用于写进标记文件，便于日后复现）。
func ensureVCRedistInstaller(ctx context.Context, cfg Config, logf func(string, ...any)) (string, string, error) {
	dest := vcRedistExePath(cfg)
	if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
		logf("使用已下载的 VC++ 运行时安装包：%s", dest)
		return dest, "", makeVCRedistReadable(cfg, dest)
	}

	srcURL := cfg.VCRedistURL
	if srcURL == "" {
		srcURL = defaultVCRedistURL
	}
	if !cfg.AutoDownload {
		return "", "", fmt.Errorf("auto_download 已关闭且本地没有 %s；"+
			"请手动下载 %s 放到该路径，或设 linux.install_vcredist: false", dest, srcURL)
	}

	// 跟随重定向拿最终地址：微软的最终 URL 路径里自带文件 sha256。
	finalURL, err := resolveFinalURL(ctx, srcURL)
	if err != nil {
		logf("解析 %s 的最终下载地址失败（%v），按原地址下载", srcURL, err)
		finalURL = srcURL
	}

	checksum := ""
	switch {
	case cfg.VCRedistSHA256 != "":
		checksum = "sha256:" + strings.ToLower(strings.TrimSpace(cfg.VCRedistSHA256))
	default:
		if h, ok := sha256FromMSDownloadURL(finalURL); ok {
			checksum = "sha256:" + h
		} else {
			logf("警告：%s 的地址里没有可用的 SHA256（自定义镜像？），本次下载不做校验；"+
				"可用 linux.vcredist_sha256 显式指定", finalURL)
		}
	}

	logf("正在下载 VC++ 运行时安装包（约 24 MiB）：%s", finalURL)
	if err := download.Fetch(ctx, download.Options{
		URL:      finalURL,
		Dest:     dest,
		Checksum: checksum,
		Resume:   true,
		Progress: downloadProgress("vc_redist", logf),
	}); err != nil {
		return "", "", fmt.Errorf("下载 VC++ 运行时安装包失败: %w", err)
	}
	return dest, checksum, makeVCRedistReadable(cfg, dest)
}

// resolveFinalURL 跟随重定向，返回最终落到的地址。
func resolveFinalURL(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := download.Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HEAD %s 返回 %s", rawURL, resp.Status)
	}
	if resp.Request == nil || resp.Request.URL == nil {
		return "", fmt.Errorf("HEAD %s 没有返回最终地址", rawURL)
	}
	return resp.Request.URL.String(), nil
}

// makeVCRedistReadable 让降权后的运行时用户能读到安装包。
//
// 用 chmod 而不是 chown：这是个只读产物，降权用户只需要读+穿过目录，不需要属主
// —— 与 ensureWorldReadExec 对 proton/umu-launcher 两个只读子树的处理同类。
func makeVCRedistReadable(cfg Config, dest string) error {
	if err := os.Chmod(vcRedistDir(cfg), 0o755); err != nil {
		return err
	}
	return os.Chmod(dest, 0o755)
}

// --- DLL override -------------------------------------------------------------

// applyVCRedistOverrides 生成 .reg 并用 prefix 自带的 regedit.exe 静默导入。
//
// winetricks 在 wow64 下会跑 32 位与 64 位两遍（只跑一遍时只进 32 位视图）；
// 我们经 umu-run 跑的是 GE-Proton 的 64 位 wine，正是需要的那一遍 ——
// ArkApi 与 ASA 服务端都是纯 x64，不需要 32 位视图。
func applyVCRedistOverrides(ctx context.Context, cfg Config, prefix string, logf func(string, ...any)) error {
	regPath := vcRedistOverrideRegPath(cfg)
	if err := os.MkdirAll(filepath.Dir(regPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(regPath, []byte(buildVCRedistOverrideReg()), 0o644); err != nil {
		return err
	}

	regedit, err := prefixRegedit(prefix)
	if err != nil {
		return err
	}

	logf("正在写入 %d 条 VC++ DLL override（%s）", len(vcRedistOverrideDLLs), vcRedistOverrideMode)
	// regedit.exe 传宿主路径（umu 对普通 exe 走 resolve(strict=True)）；
	// 它的**参数**是 Windows 路径，所以过 gamePath。
	tail, err := runInPrefix(ctx, cfg, prefix, regedit,
		[]string{"/S", gamePath(regPath)}, time.Minute, logf)
	if err != nil {
		return fmt.Errorf("regedit 导入失败%s：\n%s", msInstallerExitNote(exitCodeOf(err)), tail)
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
		if fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("Wine 前缀 %s 里找不到 regedit.exe", prefix)
}

// --- 在 prefix 里跑一个 Windows 程序 --------------------------------------------

// runInPrefix 通过 umu-run 在 prefix 里执行 exePath，返回其输出的末尾几行。
//
// 与 warmPrefix 的 wineboot 调用逐项对齐，只有两处刻意的不同：
//   - 带 UMU_RUNTIME_UPDATE=0：运行时这时早就装好了，没有理由让 umu 再去
//     repo.steampowered.com 查更新（warmPrefix 那一次故意不带，因为它必须能拉运行时）。
//   - 有硬超时：见 vcRedistInstallTimeout。
func runInPrefix(ctx context.Context, cfg Config, prefix, exePath string, args []string,
	timeout time.Duration, logf func(string, ...any), display ...displayTarget) (string, error) {

	py, err := umuInterpreter()
	if err != nil {
		return "", fmt.Errorf("failed to resolve a Python interpreter for umu-run: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var disp displayTarget
	if len(display) > 0 {
		disp = display[0]
	}

	// argv: <python> <umu-run> <exe> <args...>
	bin := py.Path
	argv := append([]string{umuRunPath(cfg), exePath}, args...)

	// inheritedEnv, not os.Environ(): 见 inheritedEnv 的注释（泄漏的
	// DBUS_SESSION_BUS_ADDRESS 会让 bwrap 直接拒绝启动整个容器）。
	env := append(inheritedEnv(),
		"WINEPREFIX="+prefix,
		"GAMEID="+cfg.GameID,
		"PROTONPATH="+protonPath(cfg),
		"UMU_RUNTIME_UPDATE=0",
		// 同 umuCommandLine：不能用 umu 默认的 waitforexitandrun。这里尤其致命 ——
		// 共享 prefix 上只要有实例在跑，`wineserver -w` 就永不返回，
		// `verify-arkapi` / EnsurePrefixVCRedist 会一路挂到下面那个硬超时
		// （vcRedistInstallTimeout，15 分钟）才报错，且错得毫无线索。
		"PROTON_VERB=run",
		// 无障碍覆盖层在这里尤其多余：override 那一步**故意不带显示**跑
		// （见 ensureVCRedist 第一步），于是 Xalia 每次必崩一次，在
		// 「正在写入 11 条 VC++ DLL override」正下方留一段 .NET 栈 ——
		// 看着像 override 失败了，其实 11/11 全写进去了。见 protonNoXalia。
		protonNoXalia,
	)
	// 用与游戏进程相同的身份运行：装出来的文件属主才对，降权后的 AsaApiLoader
	// 才读得到。同 warmPrefix。
	var cred *syscall.Credential
	if c, home, err := resolveRuntimeCredential(cfg); err != nil {
		return "", err
	} else if c != nil {
		cred = c
		env = runtimeEnv(env, home, runtimeUserName(cfg))
	}
	// 显示放最后施加，理由见 displayTarget.applyTo。
	env = disp.applyTo(env)

	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Env = env
	if cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	}

	out := &progressWriter{logf: logf}
	cmd.Stdout, cmd.Stderr = out, out
	runErr := cmd.Run()
	return out.tail(), runErr
}

// exitCodeOf 取进程退出码；-1 表示没有正常结束（被信号杀掉，含超时）。
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// --- 标记 ---------------------------------------------------------------------

func writeVCRedistMarker(prefix, exePath, checksum string) error {
	body := fmt.Sprintf("installer=%s\nchecksum=%s\ninstalled_at=%s\n",
		exePath, checksum, time.Now().Format(time.RFC3339))
	path := filepath.Join(prefix, vcRedistMarker)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	// prefix 归运行时用户，别在里面留 root 属主的文件。
	return chownPathForRuntime(path)
}
