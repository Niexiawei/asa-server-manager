package arkapimanage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/installer"
)

// 主程序安装清单：{BaseDir}/arkapi/manifest.json（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §6.5）。
//
// 只有本程序装的主程序才有清单。手工装的没有，版本显示「未知」，卸载时按固定清单处理（§6.2）。
// 插件不需要清单：版本直接读各自的 PluginInfo.json。

// Manifest 是清单文件的全部内容。
type Manifest struct {
	Core *CoreManifest `json:"core,omitempty"`
}

// CoreManifest 记录本程序装进 server-files 的主程序。
type CoreManifest struct {
	// Version 取自上传文件名，确认时可以改（主程序没有 PE 版本资源，方案 D4）
	Version     string    `json:"version"`
	Source      string    `json:"source"`
	InstalledAt time.Time `json:"installed_at"`
	// Files 是落进 Win64 的全部文件（相对 Win64，正斜杠）→ "sha256:<hex>"
	Files map[string]string `json:"files"`
	// Overwritten 是安装时覆盖掉的**非 ArkApi** 文件（游戏自带的 msvcp140.dll 等，方案 D3）
	// → 原件备份相对 {BaseDir}/arkapi 的路径。卸载时从这里还原
	Overwritten map[string]string `json:"overwritten,omitempty"`
}

func arkapiDir() string      { return filepath.Join(cfgpkg.BaseDir, "arkapi") }
func manifestPath() string   { return filepath.Join(arkapiDir(), "manifest.json") }
func coreBackupsDir() string { return filepath.Join(arkapiDir(), "backups") }

// originalsDir 存放被覆盖的游戏原件。与 backups 分开：backups 只保留最近几份，
// 原件却要一直留到卸载，放在一起的话，几次更新之后唯一的原件就被裁掉了。
func originalsDir() string { return filepath.Join(arkapiDir(), "originals") }

func serverWin64Dir() string {
	return filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame", "Binaries", "Win64")
}

// win64Path 把相对 Win64 的正斜杠路径转成绝对路径。
func win64Path(rel string) string {
	return filepath.Join(serverWin64Dir(), filepath.FromSlash(rel))
}

func readManifest() (*Manifest, error) {
	b, err := os.ReadFile(manifestPath())
	if os.IsNotExist(err) {
		return &Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("主程序安装清单 %s 已损坏（可以手工删除它，主程序随之被当成手工安装的）: %w", manifestPath(), err)
	}
	return &m, nil
}

// writeManifest 原子地写清单；Core 为空时删除清单文件。
func writeManifest(m *Manifest) error {
	if m.Core == nil {
		if err := os.Remove(manifestPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(arkapiDir(), 0755); err != nil {
		return err
	}
	tmp := manifestPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, manifestPath())
}

// lookupFold 在清单的 map 里找 rel：精确匹配优先，其次不区分大小写（Windows 上盘上的大小写
// 可能与清单里记的不同）。返回实际的键。
func lookupFold(m map[string]string, rel string) (string, bool) {
	if _, ok := m[rel]; ok {
		return rel, true
	}
	for k := range m {
		if strings.EqualFold(k, rel) {
			return k, true
		}
	}
	return "", false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

type hashEntry struct {
	size int64
	mod  time.Time
	sum  string
}

// hashCache 按 (路径, 大小, 修改时间) 缓存文件哈希：状态接口每次打开插件面板都会查，
// 而 AsaApi.pdb 一个就有 50 MB。
var hashCache sync.Map

func fileSHA256(p string) (string, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if v, ok := hashCache.Load(p); ok {
		if e := v.(hashEntry); e.size == fi.Size() && e.mod.Equal(fi.ModTime()) {
			return e.sum, nil
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := "sha256:" + hex.EncodeToString(h.Sum(nil))
	hashCache.Store(p, hashEntry{size: fi.Size(), mod: fi.ModTime(), sum: sum})
	return sum, nil
}

// CoreStatus 是主程序的当前状态（GET /api/arkapi）。
type CoreStatus struct {
	// Installed 以 server-files 里有没有 AsaApiLoader.exe 为准，与启动路径的判据相同
	Installed bool `json:"installed"`
	// Managed 表示由本程序安装（有清单）；手工装的为 false，版本未知
	Managed     bool      `json:"managed"`
	Version     string    `json:"version"`
	Source      string    `json:"source"`
	InstalledAt time.Time `json:"installed_at,omitzero"`
	// ModifiedFiles / MissingFiles 是清单里、却被外部改动或删掉的文件。config.json 不算：
	// 它本来就是给用户改的
	ModifiedFiles []string `json:"modified_files"`
	MissingFiles  []string `json:"missing_files"`
	// Overwritten 是被主程序覆盖、卸载时会还原的游戏文件
	Overwritten []string `json:"overwritten"`
	// Busy 表示 server-files 正在被改写（Steam 更新或另一个主程序操作）
	Busy bool `json:"busy"`
}

// Status 返回主程序的当前状态。
func Status() (*CoreStatus, error) {
	st := &CoreStatus{
		Installed:     installer.ArkApiInstalled(),
		ModifiedFiles: []string{},
		MissingFiles:  []string{},
		Overwritten:   []string{},
		Busy:          installer.IsUpdatingServerFiles(),
	}
	m, err := readManifest()
	if err != nil {
		return st, err
	}
	if m.Core == nil {
		return st, nil
	}
	st.Managed = true
	st.Version, st.Source, st.InstalledAt = m.Core.Version, m.Core.Source, m.Core.InstalledAt
	st.Overwritten = sortedKeys(m.Core.Overwritten)
	for _, rel := range sortedKeys(m.Core.Files) {
		if strings.EqualFold(rel, coreConfigName) {
			continue
		}
		sum, err := fileSHA256(win64Path(rel))
		switch {
		case os.IsNotExist(err):
			st.MissingFiles = append(st.MissingFiles, rel)
		case err != nil || sum != m.Core.Files[rel]:
			st.ModifiedFiles = append(st.ModifiedFiles, rel)
		}
	}
	return st, nil
}

// installedCoreVersion 返回已安装主程序的版本，未知（没装、手工装的、清单读不了）时为空。
// 供插件包校验比较 MinApiVersion。
func installedCoreVersion() string {
	if !installer.ArkApiInstalled() {
		return ""
	}
	m, err := readManifest()
	if err != nil || m.Core == nil {
		return ""
	}
	return m.Core.Version
}
