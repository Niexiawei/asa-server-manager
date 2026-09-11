package arkapimanage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"asa-server/internal/installer"
	"asa-server/internal/mirror"
	"asa-server/internal/plugindata"
	"asa-server/internal/runner"
	"asa-server/pkg/fsutil"
	"asa-server/pkg/logger"
)

// ArkApi 主程序的安装 / 更新 / 卸载（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §6.1、§6.2）。
//
// 主程序全局一份，落在 server-files 的 Win64 里，由各实例的 EnableAsaPlugin 决定用不用。
// 包里附带的插件**不写进 server-files**（新布局下那里只留一个空的 Plugins 目录），
// 由用户在确认时挑选要装进哪些实例，按插件安装的规则逐实例执行。
//
// 三条约束：
//   - 被换下来的文件一律进备份（{BaseDir}/arkapi/backups/core-<时间戳>/），不直接删（§6.0）；
//   - 覆盖游戏自带的同名文件（msvcp140.dll）前，先把原件存进 {BaseDir}/arkapi/originals，
//     卸载时还原（方案 D3）；
//   - 逐文件搬动并记日志，任何一步失败都按日志倒序搬回去，server-files 回到操作前的样子。
//
// 整个换位在 installer.BeginArkApiWrite（与 Steam 更新互斥、期间拒绝启动实例）与
// mirror.WithSyncLock（不让镜像同步读到半新半旧的 server-files）之下进行。
// 运行中的实例不受影响：镜像里的 Win64 是真实拷贝，它们下次启动才同步到新版本。

const (
	kindCore       = "core"
	coreConfigName = "config.json"
	arkApiDirName  = "ArkApi"
	libDirName     = "Lib"
	maxVersionLen  = 64
)

// knownArkApiRootFiles 是 ArkApi 放在 Win64 根目录的文件。msvcp140.dll 不在其中：
// 它与游戏自带的文件同名（方案 D3）。两个用途：判断覆盖的是不是游戏文件；没有清单时卸载按它来。
var knownArkApiRootFiles = []string{
	"AsaApiLoader.exe", "AsaApiLoader.pdb", "libcrypto-3-x64.dll", "libssl-3-x64.dll", "msdia140.dll",
}

// coreBackupName 严格匹配主程序备份目录，裁剪时不会碰到同一目录下的 legacy-server-plugins-*。
var coreBackupName = regexp.MustCompile(`^core-\d{8}-\d{6}(-\d+)?$`)

// coreFaultHook 供测试在换位的某一步注入失败，验证回滚。
var coreFaultHook func(rel string) error

// CoreStage 是上传主程序包之后返回的校验报告。校验失败时 Token 为空。
type CoreStage struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	Kind      string    `json:"kind"`
	*CoreReport
	// Action 是 install（server-files 里还没有主程序）或 update
	Action  string      `json:"action"`
	Current *CoreStatus `json:"current"`
	// Overwrites 是会被覆盖的非 ArkApi 文件：原件先备份，卸载主程序时还原（方案 D3）
	Overwrites []string `json:"overwrites"`
	// BundledTargets 是附带插件各自的目标实例表（按插件名）。前端默认一个都不勾（§6.1）
	BundledTargets map[string][]PluginTarget `json:"bundled_targets"`
}

// CoreResult 是主程序安装 / 更新的结果。
type CoreResult struct {
	Action   string   `json:"action"`
	Version  string   `json:"version"`
	Warnings []string `json:"warnings"`
	// Backup 是被换下来的旧文件所在目录，没有换下任何文件时为空
	Backup string `json:"backup,omitempty"`
	// Results 是附带插件在各实例上的安装结果（Result.Plugin 标明是哪个插件）
	Results []Result `json:"results"`
}

// CoreUninstallResult 是主程序卸载的结果。
type CoreUninstallResult struct {
	// Managed 表示按清单卸载；false 表示手工装的，按固定清单卸载
	Managed  bool     `json:"managed"`
	Removed  []string `json:"removed"`
	Restored []string `json:"restored"`
	Warnings []string `json:"warnings"`
	Backup   string   `json:"backup,omitempty"`
}

// StageCore 接收一个主程序包：落进暂存区、解压、校验，返回报告。
// uploadName 是上传时的文件名，版本号从它提取。
//
// 包不合格时返回的报告里 Errors 非空、Token 为空，暂存区已经清掉；只有服务端或传输错误才返回 error。
func StageCore(src io.Reader, uploadName string) (*CoreStage, error) {
	sp, err := newStage(kindCore)
	if err != nil {
		return nil, err
	}
	entries, extractErr, err := sp.receive(src)
	if err != nil {
		sp.remove()
		return nil, err
	}

	var rep *CoreReport
	if extractErr != nil {
		rep = &CoreReport{Files: []FileEntry{}, Bundled: []*PluginReport{}, Errors: []string{extractErr.Error()}, Warnings: []string{}}
	} else {
		rep = ValidateCorePackage(sp.extractDir(), entries, uploadName)
	}
	stage := &CoreStage{Kind: kindCore, CoreReport: rep, Overwrites: []string{}, BundledTargets: map[string][]PluginTarget{}}

	cur, err := Status()
	stage.Current = cur
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
	}
	if len(rep.Errors) > 0 {
		sp.remove()
		logger.Infof("主程序包 %s 未通过校验: %v", uploadName, rep.Errors)
		return stage, nil
	}

	stage.Action = "install"
	if cur.Installed {
		stage.Action = "update"
	}
	items, ignored := coreInstallItems(rep)
	if len(ignored) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("以下 %d 个文件不在 ArkApi 的安装范围内（根目录、ArkApi/、Lib/），不会被安装：%s",
			len(ignored), strings.Join(ignored, "、")))
	}
	old := &CoreManifest{}
	if m, err := readManifest(); err == nil && m.Core != nil {
		old = m.Core
	}
	for _, it := range items {
		if _, carried := lookupFold(old.Overwritten, it.rel); !carried && isGameFileCandidate(it.rel, old) && fsutil.FileExists(win64Path(it.rel)) {
			stage.Overwrites = append(stage.Overwrites, it.rel)
		}
	}
	for _, pr := range rep.Bundled {
		if len(pr.Errors) == 0 {
			stage.BundledTargets[pr.Name] = pluginTargets(pr)
		}
	}

	sp.core, sp.source = rep, filepath.Base(uploadName)
	register(sp)
	stage.Token, stage.ExpiresAt = sp.token, sp.expiresAt
	logger.Infof("主程序包 %s 已暂存：版本 %s，附带插件 %d 个", uploadName, rep.Version, len(rep.Bundled))
	return stage, nil
}

// ApplyCore 安装或更新暂存的主程序，然后把 bundled 里选中的附带插件装进各自列出的实例。
//
// version 为 nil 时沿用从文件名提取的版本号。bundled 的键是插件名、值是目标实例，没列出的插件
// 不装、没列出的实例不动（§1.1）；restoreFromBackup 的语义同插件安装。
// 暂存包在真正开始换位之后即告用完，不论成败；server-files 正忙时原样保留，可以稍后再确认。
func ApplyCore(token string, version *string, bundled map[string][]string, restoreFromBackup bool) (*CoreResult, error) {
	end, err := installer.BeginArkApiWrite()
	if err != nil {
		return nil, err
	}
	sp, err := takeIf(token, kindCore, func(sp *stagedPackage) error {
		return checkBundledSelection(sp.core, bundled)
	})
	if err != nil {
		end()
		return nil, err
	}
	defer sp.remove()

	ver := sp.core.Version
	if version != nil {
		ver = strings.TrimSpace(*version)
	}
	if len(ver) > maxVersionLen {
		end()
		return nil, fmt.Errorf("版本号过长（最多 %d 个字符）", maxVersionLen)
	}

	var res *CoreResult
	err = mirror.WithSyncLock(func() error {
		var err error
		res, err = installCore(sp.core, ver, sp.source)
		return err
	})
	end()
	if err != nil {
		logger.Errorf("ArkApi 主程序安装失败: %v", err)
		return nil, err
	}
	// Linux：从暂存区挪过来的文件不继承 server-files 的默认 ACL / setgid（方案 §10）
	if err := runner.PrepareSharedTree(serverWin64Dir()); err != nil {
		logger.Warnf("为运行时用户整理 %s 的权限失败: %v", serverWin64Dir(), err)
	}
	logger.Infof("ArkApi 主程序 %s 完成（%s），版本 %s", res.Action, sp.source, displayVersion(ver))

	for _, pr := range sp.core.Bundled {
		for _, inst := range dedupe(bundled[pr.Name]) {
			r := applyPluginTo(inst, pr, restoreFromBackup)
			r.Plugin = pr.Name
			if r.OK {
				logger.Infof("实例 %s：附带插件 %s 完成（%s）", inst, pr.Name, r.Action)
			} else {
				logger.Warnf("实例 %s：附带插件 %s 安装失败: %s", inst, pr.Name, r.Error)
			}
			res.Results = append(res.Results, r)
		}
	}
	return res, nil
}

// checkBundledSelection 在动 server-files 之前确认附带插件的选择都能装：选了一个包里没有、
// 或没通过校验的插件，就整个不做。
func checkBundledSelection(rep *CoreReport, bundled map[string][]string) error {
	for name, targets := range bundled {
		if len(targets) == 0 {
			continue
		}
		i := slices.IndexFunc(rep.Bundled, func(pr *PluginReport) bool { return pr.Name == name })
		if i < 0 {
			return fmt.Errorf("主程序包里没有附带插件 %s", name)
		}
		if errs := rep.Bundled[i].Errors; len(errs) > 0 {
			return fmt.Errorf("附带插件 %s 未通过校验，不能安装: %s", name, errs[0])
		}
	}
	return nil
}

// installItem 是要落进 Win64 的一个文件。
type installItem struct {
	src string // 暂存区里的绝对路径
	rel string // 相对 Win64 的正斜杠路径
}

// coreInstallItems 按 §6.1 的表把包内文件映射到 Win64：根目录的文件、ArkApi/ 下（除 Plugins/、
// Cache/）、Lib/ 下。其余的不装，返回在 ignored 里。
//
// ArkApi/、Lib/ 这两级目录名取盘上的实际大小写：手工装出来的 arkapi/ 在 Linux 上与 ArkApi/
// 是两个目录，照常量写会多出第二份主程序。
func coreInstallItems(rep *CoreReport) (items []installItem, ignored []string) {
	win64 := serverWin64Dir()
	for _, f := range rep.Files {
		parts := strings.Split(f.Path, "/")
		var rel string
		switch {
		case len(parts) == 1:
			rel = f.Path
		case strings.EqualFold(parts[0], arkApiDirName):
			if strings.EqualFold(parts[1], "Plugins") {
				continue // 附带插件，按实例装
			}
			if strings.EqualFold(parts[1], "Cache") {
				ignored = append(ignored, f.Path) // 运行期缓存，归 ArkApi 与缓存预取管
				continue
			}
			rel = diskName(win64, arkApiDirName) + "/" + strings.Join(parts[1:], "/")
		case strings.EqualFold(parts[0], libDirName):
			rel = diskName(win64, libDirName) + "/" + strings.Join(parts[1:], "/")
		default:
			ignored = append(ignored, f.Path)
			continue
		}
		items = append(items, installItem{src: filepath.Join(rep.root, filepath.FromSlash(f.Path)), rel: rel})
	}
	return items, ignored
}

// isGameFileCandidate 报告覆盖 rel 时它是不是**游戏的**文件（要备份原件、卸载时还原）：
// Win64 根目录下、不是 ArkApi 自己的文件、也不是上一次本程序装进去的。
func isGameFileCandidate(rel string, old *CoreManifest) bool {
	if strings.Contains(rel, "/") || strings.EqualFold(rel, coreConfigName) || isKnownArkApiRootFile(rel) {
		return false
	}
	_, ours := lookupFold(old.Files, rel)
	return !ours
}

func isKnownArkApiRootFile(name string) bool {
	return slices.ContainsFunc(knownArkApiRootFiles, func(k string) bool { return strings.EqualFold(k, name) })
}

func installCore(rep *CoreReport, version, source string) (*CoreResult, error) {
	m, err := readManifest()
	if err != nil {
		return nil, err
	}
	old := m.Core
	if old == nil {
		old = &CoreManifest{}
	}
	res := &CoreResult{Action: "install", Version: version, Warnings: []string{}, Results: []Result{}}
	if installer.ArkApiInstalled() {
		res.Action = "update"
	}

	t, err := newCoreTxn()
	if err != nil {
		return nil, err
	}
	next := &CoreManifest{
		Version:     version,
		Source:      source,
		InstalledAt: time.Now(),
		Files:       map[string]string{},
		Overwritten: map[string]string{},
	}
	items, _ := coreInstallItems(rep)

	err = t.run(func() error {
		var placed []string
		for _, it := range items {
			if coreFaultHook != nil {
				if err := coreFaultHook(it.rel); err != nil {
					return err
				}
			}
			if strings.EqualFold(it.rel, coreConfigName) {
				ok, err := t.placeConfig(it, &res.Warnings)
				if err != nil {
					return err
				}
				if ok {
					placed = append(placed, it.rel)
				}
				continue
			}

			keepAt := ""
			if k, carried := lookupFold(old.Overwritten, it.rel); carried {
				// 原件早在第一次安装时就存好了，这次换下来的是上一版 ArkApi 自己的文件
				next.Overwritten[it.rel] = old.Overwritten[k]
			} else if isGameFileCandidate(it.rel, old) && fsutil.FileExists(win64Path(it.rel)) {
				keepAt = filepath.Join(originalsDir(), filepath.FromSlash(it.rel))
				if fsutil.FileExists(keepAt) {
					// 上一次操作残留的原件：挪进这次的备份，不覆盖
					if err := t.move(keepAt, filepath.Join(t.backupDir, "stale-originals", filepath.FromSlash(it.rel))); err != nil {
						return err
					}
				}
				next.Overwritten[it.rel] = filepath.ToSlash(mustRel(arkapiDir(), keepAt))
			}
			if err := t.replace(it.rel, it.src, keepAt); err != nil {
				return err
			}
			placed = append(placed, it.rel)
		}

		// 旧清单里有、新包里没有的文件：移入备份；它若覆盖过游戏原件，把原件放回去
		for _, rel := range sortedKeys(old.Files) {
			if slices.ContainsFunc(items, func(it installItem) bool { return strings.EqualFold(it.rel, rel) }) {
				continue
			}
			if k, ok := lookupFold(old.Overwritten, rel); ok {
				restored, err := t.restoreOriginal(rel, old.Overwritten[k], &res.Warnings)
				if err != nil {
					return err
				}
				if !restored {
					res.Warnings = append(res.Warnings, fmt.Sprintf("新版本不再包含 %s，但它的游戏原件备份不见了，已移入备份", rel))
				}
				continue
			}
			if err := t.replace(rel, "", ""); err != nil {
				return err
			}
		}

		for _, rel := range placed {
			sum, err := fileSHA256(win64Path(rel))
			if err != nil {
				return err
			}
			next.Files[rel] = sum
		}
		if len(next.Overwritten) == 0 {
			next.Overwritten = nil
		}
		m.Core = next
		return writeManifest(m)
	})
	if err != nil {
		return nil, err
	}
	res.Backup = t.finish()
	return res, nil
}

// placeConfig 放置 ArkApi 自己的 config.json：目标不存在就直接落地；存在就合并——旧值优先、
// 新版本的新键并入、保序（§6.1）。旧文件不是 JSON 对象时原样保留，不猜。
func (t *coreTxn) placeConfig(it installItem, warnings *[]string) (placed bool, err error) {
	dst := win64Path(it.rel)
	cur, err := os.ReadFile(dst)
	if os.IsNotExist(err) {
		return true, t.replace(it.rel, it.src, "")
	}
	if err != nil {
		return false, err
	}
	incoming, err := os.ReadFile(it.src)
	if err != nil {
		return false, err
	}
	merged, err := plugindata.MergeConfigJSON(cur, incoming)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("现有的 %s 不是 JSON 对象，保留原样，没有合并新版本的配置项: %v", it.rel, err))
		return false, nil
	}
	// 暂存区是一次性的，合并结果直接写回暂存的那份再换位
	if err := os.WriteFile(it.src, merged, 0644); err != nil {
		return false, err
	}
	return true, t.replace(it.rel, it.src, "")
}

// UninstallCore 卸载主程序（§6.2）。各实例的插件目录完全不动：镜像里的 Plugins junction
// 在下次同步时被当作多余条目移除（只删链接），主程序装回来之后自动恢复。
func UninstallCore() (*CoreUninstallResult, error) {
	end, err := installer.BeginArkApiWrite()
	if err != nil {
		return nil, err
	}
	var res *CoreUninstallResult
	err = mirror.WithSyncLock(func() error {
		var err error
		res, err = uninstallCore()
		return err
	})
	end()
	if err != nil {
		logger.Errorf("ArkApi 主程序卸载失败: %v", err)
		return nil, err
	}
	if len(res.Restored) > 0 {
		if err := runner.PrepareSharedTree(serverWin64Dir()); err != nil {
			logger.Warnf("为运行时用户整理 %s 的权限失败: %v", serverWin64Dir(), err)
		}
	}
	logger.Infof("ArkApi 主程序已卸载：移除 %d 个文件，还原 %d 个游戏文件，备份在 %s",
		len(res.Removed), len(res.Restored), res.Backup)
	return res, nil
}

func uninstallCore() (*CoreUninstallResult, error) {
	m, err := readManifest()
	if err != nil {
		return nil, err
	}
	win64 := serverWin64Dir()
	res := &CoreUninstallResult{Managed: m.Core != nil, Removed: []string{}, Restored: []string{}, Warnings: []string{}}
	arkDir := filepath.Join(win64, diskName(win64, arkApiDirName))
	if m.Core == nil && !installer.ArkApiInstalled() && !isDirPath(arkDir) {
		return nil, errors.New("没有安装 ArkApi 主程序")
	}

	t, err := newCoreTxn()
	if err != nil {
		return nil, err
	}
	err = t.run(func() error {
		remove := func(rel string) error {
			if !fsutil.FileExists(win64Path(rel)) {
				return nil
			}
			if err := t.replace(rel, "", ""); err != nil {
				return err
			}
			res.Removed = append(res.Removed, rel)
			return nil
		}

		if m.Core != nil {
			for _, rel := range sortedKeys(m.Core.Files) {
				k, overwrote := lookupFold(m.Core.Overwritten, rel)
				if !overwrote {
					if err := remove(rel); err != nil {
						return err
					}
					continue
				}
				// 覆盖过游戏原件的文件：还是我们装的那一份才还原。被外部换过（多半是 Steam 校验
				// 已经还原了游戏版本）就保留现状，拿旧原件去覆盖只会把游戏文件退回旧版
				if sum, err := fileSHA256(win64Path(rel)); err == nil && sum != m.Core.Files[rel] {
					res.Warnings = append(res.Warnings, fmt.Sprintf(
						"%s 在安装之后被替换过（可能是 Steam 校验还原了游戏版本），保留现状；安装前的原件已移入备份", rel))
					orig := filepath.Join(arkapiDir(), filepath.FromSlash(m.Core.Overwritten[k]))
					if fsutil.FileExists(orig) {
						if err := t.move(orig, filepath.Join(t.backupDir, "originals", filepath.FromSlash(rel))); err != nil {
							return err
						}
					}
					continue
				}
				restored, err := t.restoreOriginal(rel, m.Core.Overwritten[k], &res.Warnings)
				if err != nil {
					return err
				}
				if restored {
					res.Restored = append(res.Restored, rel)
				} else {
					res.Removed = append(res.Removed, rel)
				}
			}
		} else {
			for _, name := range knownArkApiRootFiles {
				if err := remove(diskName(win64, name)); err != nil {
					return err
				}
			}
			if cfg := diskName(win64, coreConfigName); looksLikeArkApiConfig(win64Path(cfg)) {
				if err := remove(cfg); err != nil {
					return err
				}
			}
			lib := diskName(win64, libDirName)
			if err := remove(lib + "/" + diskName(filepath.Join(win64, lib), "AsaApi.lib")); err != nil {
				return err
			}
			if msvcp := diskName(win64, "msvcp140.dll"); fsutil.FileExists(win64Path(msvcp)) {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"主程序不是本程序安装的，无法判断 %s 是游戏原版还是 ArkApi 带来的，已保留。需要还原游戏原版时，请用 Steam 校验服务端文件", msvcp))
			}
		}

		// ArkApi/ 里剩下的（Cache/、手工放进去的文件）整个移入备份。Plugins/ 在新布局下是空的；
		// 不空说明还有实例没迁移、正从这里加载插件，保留不动
		entries, _ := os.ReadDir(arkDir)
		for _, e := range entries {
			p := filepath.Join(arkDir, e.Name())
			if strings.EqualFold(e.Name(), "Plugins") && !dirEmpty(p) {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"%s 里还有插件（有实例尚未迁移到独立插件目录，正从这里加载插件），保留不动", p))
				continue
			}
			rel := filepath.Base(arkDir) + "/" + e.Name()
			if err := t.move(p, filepath.Join(t.backupDir, "previous", filepath.FromSlash(rel))); err != nil {
				return err
			}
			res.Removed = append(res.Removed, rel)
		}

		m.Core = nil
		return writeManifest(m)
	})
	if err != nil {
		return nil, err
	}
	// 搬空了的目录最后才删：回滚要往里搬回东西，删早了父目录就没了
	for _, dir := range []string{arkDir, filepath.Join(win64, diskName(win64, libDirName)), originalsDir()} {
		_ = os.Remove(dir) // 不空时删不掉，正是想要的
	}
	res.Backup = t.finish()
	return res, nil
}

// restoreOriginal 把 rel 现在的文件（ArkApi 的版本）移入备份，再把 origRel 处的游戏原件放回去。
// 原件不见了时只做前一半，返回 restored=false。
func (t *coreTxn) restoreOriginal(rel, origRel string, warnings *[]string) (restored bool, err error) {
	orig := filepath.Join(arkapiDir(), filepath.FromSlash(origRel))
	if !fsutil.FileExists(orig) {
		*warnings = append(*warnings, fmt.Sprintf("%s 的游戏原件备份（%s）不见了，无法还原", rel, orig))
		if fsutil.FileExists(win64Path(rel)) {
			return false, t.replace(rel, "", "")
		}
		return false, nil
	}
	return true, t.replace(rel, orig, "")
}

// looksLikeArkApiConfig 判断一个 config.json 是不是 ArkApi 的：顶层有 settings，
// 其中含 AutomaticPluginReloading 或 AutomaticCacheDownload（§6.2）。
func looksLikeArkApiConfig(p string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var v struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if json.Unmarshal(stripBOM(b), &v) != nil {
		return false
	}
	_, a := v.Settings["AutomaticPluginReloading"]
	_, c := v.Settings["AutomaticCacheDownload"]
	return a || c
}

func stripBOM(b []byte) []byte {
	return []byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf"))
}

func displayVersion(v string) string {
	if v == "" {
		return "未知"
	}
	return v
}

// ---- 带回滚日志的搬动 ----

// coreTxn 记录一次主程序操作里的每一次搬动，失败时倒序搬回去。
type coreTxn struct {
	backupDir   string
	journal     []moveRec
	createdDirs []string
}

type moveRec struct{ from, to string }

func newCoreTxn() (*coreTxn, error) {
	root := coreBackupsDir()
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	return &coreTxn{backupDir: uniquePath(filepath.Join(root, "core-"+time.Now().Format("20060102-150405")))}, nil
}

// run 执行 fn；fn 失败时回滚它做过的全部搬动。
func (t *coreTxn) run(fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}
	if rbErr := t.rollback(); rbErr != nil {
		return fmt.Errorf("%w；回滚未能完成，请手工检查 server-files（被换下的文件在 %s）: %v", err, t.backupDir, rbErr)
	}
	return fmt.Errorf("%w（已回滚，server-files 未改动）", err)
}

// move 把 from 挪到 to（to 必须不存在），记入日志。
func (t *coreTxn) move(from, to string) error {
	if err := t.mkdirs(filepath.Dir(to)); err != nil {
		return err
	}
	if err := movePath(from, to); err != nil {
		return err
	}
	t.journal = append(t.journal, moveRec{from: from, to: to})
	return nil
}

// replace 把 Win64 里 rel 现有的文件挪到 keepAt（为空时挪进备份的 previous/<rel>），
// 再把 src 挪到位（src 为空表示只移走）。
func (t *coreTxn) replace(rel, src, keepAt string) error {
	dst := win64Path(rel)
	if _, err := os.Lstat(dst); err == nil {
		if keepAt == "" {
			keepAt = filepath.Join(t.backupDir, "previous", filepath.FromSlash(rel))
		}
		if err := t.move(dst, keepAt); err != nil {
			return fmt.Errorf("移走 %s 失败（文件可能被占用）: %w", rel, err)
		}
	}
	if src == "" {
		return nil
	}
	if err := t.move(src, dst); err != nil {
		return fmt.Errorf("放置 %s 失败: %w", rel, err)
	}
	return nil
}

func (t *coreTxn) rollback() error {
	var errs []error
	for i := len(t.journal) - 1; i >= 0; i-- {
		r := t.journal[i]
		if err := movePath(r.to, r.from); err != nil {
			errs = append(errs, fmt.Errorf("%s → %s: %w", r.to, r.from, err))
		}
	}
	t.journal = nil
	for i := len(t.createdDirs) - 1; i >= 0; i-- {
		_ = os.Remove(t.createdDirs[i]) // 只删空的：搬回之后它们应当是空的
	}
	return errors.Join(errs...)
}

// mkdirs 同 os.MkdirAll，另外记下新建了哪些目录，回滚时删掉。
func (t *coreTxn) mkdirs(dir string) error {
	var missing []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil || filepath.Dir(d) == d {
			break
		}
		missing = append(missing, d)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		t.createdDirs = append(t.createdDirs, missing[i])
	}
	return nil
}

// finish 在成功之后调用：裁剪旧备份，返回这次的备份目录（没有换下任何东西时为空）。
func (t *coreTxn) finish() string {
	pruneCoreBackups()
	if isDirPath(t.backupDir) {
		return t.backupDir
	}
	return ""
}

// pruneCoreBackups 只保留最近 keepBackups 份主程序备份。
func pruneCoreBackups() {
	entries, err := os.ReadDir(coreBackupsDir())
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && coreBackupName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names) // 时间戳定宽，按名字排就是按时间排
	for len(names) > keepBackups {
		p := filepath.Join(coreBackupsDir(), names[0])
		if err := os.RemoveAll(p); err != nil {
			logger.Warnf("清理旧的主程序备份 %s 失败: %v", p, err)
		}
		names = names[1:]
	}
}

// movePath 把文件或目录 src 挪到 dst（dst 不得存在）。先 rename；rename 失败时（典型是跨卷：
// server-files 可能是指到别的盘的链接）退化为复制后删除源，删不掉源就撤掉复制品并报错。
func movePath(src, dst string) error {
	renameErr := os.Rename(src, dst)
	if renameErr == nil {
		return nil
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return renameErr
	}
	if _, err := os.Lstat(dst); err == nil {
		return renameErr
	}
	if fi.IsDir() {
		err = fsutil.CopyDir(src, dst)
	} else {
		err = fsutil.CopyFile(src, dst)
	}
	if err == nil {
		if fi.IsDir() {
			err = os.RemoveAll(src)
		} else {
			err = os.Remove(src)
		}
	}
	if err != nil {
		_ = os.RemoveAll(dst)
		return fmt.Errorf("%w（复制方式也失败: %v）", renameErr, err)
	}
	return nil
}

// diskName 返回 parent 下与 want 不区分大小写相同的那个条目的实际名字；没有时返回 want。
func diskName(parent, want string) string {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return want
	}
	for _, e := range entries {
		if e.Name() == want {
			return want
		}
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), want) {
			return e.Name()
		}
	}
	return want
}

// uniquePath 返回一个尚不存在的路径：base 已存在时加 -2、-3……
func uniquePath(base string) string {
	p := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p
		}
		p = fmt.Sprintf("%s-%d", base, i)
	}
}

func mustRel(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

func isDirPath(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func dirEmpty(p string) bool {
	entries, err := os.ReadDir(p)
	return err == nil && len(entries) == 0
}
