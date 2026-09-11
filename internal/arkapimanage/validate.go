package arkapimanage

import (
	"debug/pe"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"asa-server/internal/plugindata"
	"asa-server/pkg/archive"
)

// 包校验（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §5）。
//
// 输入是 pkg/archive.ExtractZip 解压出来的目录与条目清单：通用的 zip 安全规则
// （路径穿越、符号链接、大小写重复、上限）在那里已经挡过，这里只管「是不是一个合格的
// ArkApi 包」。纯函数：只读解压目录，不碰 server-files 与实例目录。
//
// 错误（Errors）阻断安装；警告（Warnings）只提示。

// FileEntry 是报告里列出的一个文件。
type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// PluginReport 是一个插件（上传的插件包，或主程序包里附带的插件）的校验结果。
type PluginReport struct {
	Name          string      `json:"name"`
	FullName      string      `json:"full_name"`
	Version       string      `json:"version"`
	Description   string      `json:"description"`
	MinApiVersion string      `json:"min_api_version"`
	Dependencies  []string    `json:"dependencies"`
	Files         []FileEntry `json:"files"`
	// Ignored 是包里位于插件目录之外、不会被安装的文件
	Ignored  []string `json:"ignored,omitempty"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`

	// root 是插件根在解压目录里的绝对路径，安装时从这里复制
	root string
}

// PluginCheck 是插件包校验需要的外部事实。
type PluginCheck struct {
	// Expect 非空时，包里解析出的插件名必须等于它（从某一行点「更新」上传的包）
	Expect string
	// CoreInstalled 报告 server-files 里有没有 ArkApi 主程序
	CoreInstalled bool
	// CoreVersion 是已安装主程序的版本，未知时为空（此时不检查 MinApiVersion）
	CoreVersion string
}

const pluginInfoName = "PluginInfo.json"

// ValidatePluginPackage 校验一个插件包（§5.3）。
func ValidatePluginPackage(extractDir string, entries []archive.Entry, chk PluginCheck) *PluginReport {
	rep := &PluginReport{Files: []FileEntry{}, Errors: []string{}, Warnings: []string{}, Dependencies: []string{}}
	files := fileEntries(entries)

	for _, f := range files {
		if b := path.Base(f.Path); strings.EqualFold(b, "AsaApiLoader.exe") || strings.EqualFold(b, "AsaApi.dll") {
			rep.addError("这是 ArkApi 主程序包，不是插件包，请用「上传主程序」安装")
			return rep
		}
	}

	var infos []string
	for _, f := range files {
		if strings.EqualFold(path.Base(f.Path), pluginInfoName) {
			infos = append(infos, f.Path)
		}
	}
	switch len(infos) {
	case 0:
		rep.addError("不是 ArkApi 插件包：包里没有 PluginInfo.json")
		return rep
	case 1:
	default:
		rep.addError(fmt.Sprintf("一个包只能包含一个插件，这个包里有 %d 个 PluginInfo.json：%s",
			len(infos), strings.Join(infos, "、")))
		return rep
	}

	rootRel := path.Dir(infos[0])
	inRoot, ignored := splitUnder(files, rootRel)
	rep.Ignored = ignored

	name := path.Base(rootRel)
	if rootRel == "." {
		// 平铺在 zip 根：插件名取「有同名 .pdb 的那个 .dll」
		n, err := flatPluginName(inRoot)
		if err != nil {
			rep.addError(err.Error())
			rep.Files = inRoot
			return rep
		}
		name = n
	}

	validatePluginDir(rep, filepath.Join(extractDir, filepath.FromSlash(rootRel)), name, inRoot)
	if len(ignored) > 0 {
		rep.addWarning(fmt.Sprintf("以下 %d 个文件不在插件目录内，不会被安装：%s", len(ignored), strings.Join(ignored, "、")))
	}
	if !chk.CoreInstalled {
		rep.addError("尚未安装 ArkApi 主程序，请先安装主程序再添加插件")
	}
	if chk.Expect != "" && rep.Name != chk.Expect {
		rep.addError(fmt.Sprintf("包里的插件是 %s，不是要更新的 %s", rep.Name, chk.Expect))
	}
	if chk.CoreVersion != "" && rep.MinApiVersion != "" && compareAPIVersions(rep.MinApiVersion, chk.CoreVersion) > 0 {
		rep.addWarning(fmt.Sprintf("插件要求 ArkApi 版本不低于 %s，已安装的是 %s", rep.MinApiVersion, chk.CoreVersion))
	}
	return rep
}

// validatePluginDir 是插件包与主程序附带插件共用的校验：dir 是插件根，name 是插件名，
// files 是插件根下的文件（相对插件根）。
func validatePluginDir(rep *PluginReport, dir, name string, files []FileEntry) {
	rep.Name = name
	rep.Files = files
	rep.root = dir

	if err := plugindata.ValidatePluginName(name); err != nil {
		rep.addError(err.Error())
		return
	}

	dll := name + ".dll"
	switch actual, ok := findFold(files, dll); {
	case !ok:
		rep.addError(fmt.Sprintf("缺少 %s（ArkApi 按 <目录名>/<目录名>.dll 加载插件）", dll))
	case actual != dll:
		rep.addError(fmt.Sprintf("插件 dll 的文件名必须与目录名一致（包括大小写）：应为 %s，实际为 %s", dll, actual))
	default:
		if err := checkAMD64PE(filepath.Join(dir, dll)); err != nil {
			rep.addError(fmt.Sprintf("%s 不是有效的 64 位 Windows DLL：%v", dll, err))
		}
	}
	if _, ok := findFold(files, name+".pdb"); !ok {
		rep.addError(fmt.Sprintf("缺少 %s.pdb", name))
	}

	meta, err := plugindata.ReadPluginMeta(dir)
	if err != nil {
		rep.addError(fmt.Sprintf("PluginInfo.json 无效：%v", err))
		return
	}
	if strings.TrimSpace(meta.FullName) == "" {
		rep.addError("PluginInfo.json 缺少 FullName")
	}
	rep.FullName = meta.FullName
	rep.Version = meta.Version
	rep.Description = meta.Description
	rep.MinApiVersion = meta.MinApiVersion
	if meta.Dependencies != nil {
		rep.Dependencies = meta.Dependencies
	}
	// 只警告不阻断（方案 D1）：官方包自带的 Permissions 就是 "Ark:SA Permissions"
	if meta.FullName != "" && meta.FullName != name {
		rep.addWarning(fmt.Sprintf("PluginInfo.json 的 FullName（%s）与插件目录名（%s）不同。ArkApi 按目录名加载插件，这通常没有问题",
			meta.FullName, name))
	}
}

// flatPluginName 在平铺结构里找插件名：唯一一个带同名 .pdb 的 .dll 的主名。
func flatPluginName(files []FileEntry) (string, error) {
	var names []string
	for _, f := range files {
		if strings.Contains(f.Path, "/") || !strings.EqualFold(path.Ext(f.Path), ".dll") {
			continue
		}
		stem := strings.TrimSuffix(f.Path, path.Ext(f.Path))
		if _, ok := findFold(files, stem+".pdb"); ok {
			names = append(names, stem)
		}
	}
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		return "", fmt.Errorf("无法确定插件名：包里的文件平铺在根目录，但没有找到带同名 .pdb 的 .dll。请把插件放在以插件名命名的目录里再打包")
	default:
		return "", fmt.Errorf("无法确定插件名：根目录下有多个带 .pdb 的 dll（%s）。请把插件放在以插件名命名的目录里再打包",
			strings.Join(names, "、"))
	}
}

// CoreReport 是主程序包的校验结果（§5.2）。
type CoreReport struct {
	// Version 从上传文件名提取（AsaApi_2.03.zip → 2.03），确认时可以修改。
	// 主程序的 dll/exe 都没有 PE 版本资源，别处拿不到（方案 D4）。
	Version  string          `json:"version"`
	Files    []FileEntry     `json:"files"`
	Ignored  []string        `json:"ignored,omitempty"`
	Bundled  []*PluginReport `json:"bundled"`
	Errors   []string        `json:"errors"`
	Warnings []string        `json:"warnings"`

	root string
}

var coreRequired = []string{
	"AsaApiLoader.exe",
	"AsaApiLoader.pdb",
	"ArkApi/AsaApi.dll",
	"ArkApi/AsaApi.pdb",
	"Lib/AsaApi.lib",
}

// ValidateCorePackage 校验一个主程序包。uploadName 是上传时的文件名，用来提取版本号。
func ValidateCorePackage(extractDir string, entries []archive.Entry, uploadName string) *CoreReport {
	rep := &CoreReport{Files: []FileEntry{}, Bundled: []*PluginReport{}, Errors: []string{}, Warnings: []string{}}
	rep.Version = versionFromFileName(uploadName)
	files := fileEntries(entries)

	var loaders []string
	for _, f := range files {
		if strings.EqualFold(path.Base(f.Path), "AsaApiLoader.exe") {
			loaders = append(loaders, f.Path)
		}
	}
	switch len(loaders) {
	case 0:
		rep.Errors = append(rep.Errors, "不是 ArkApi 主程序包：包里没有 AsaApiLoader.exe")
		return rep
	case 1:
	default:
		rep.Errors = append(rep.Errors, fmt.Sprintf("包里有 %d 个 AsaApiLoader.exe，无法确定主程序在哪：%s",
			len(loaders), strings.Join(loaders, "、")))
		return rep
	}

	rootRel := path.Dir(loaders[0])
	rep.root = filepath.Join(extractDir, filepath.FromSlash(rootRel))
	inRoot, ignored := splitUnder(files, rootRel)
	rep.Files = inRoot
	rep.Ignored = ignored
	if len(ignored) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("以下 %d 个文件不在主程序目录内，不会被安装：%s",
			len(ignored), strings.Join(ignored, "、")))
	}

	for _, req := range coreRequired {
		if _, ok := findFold(inRoot, req); !ok {
			rep.Errors = append(rep.Errors, "缺少 "+req)
		}
	}
	for _, exe := range []string{"AsaApiLoader.exe", "ArkApi/AsaApi.dll"} {
		if actual, ok := findFold(inRoot, exe); ok {
			if err := checkAMD64PE(filepath.Join(rep.root, filepath.FromSlash(actual))); err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s 不是有效的 64 位 Windows 程序：%v", exe, err))
			}
		}
	}

	// 附带插件：ArkApi/Plugins/<X>/...，逐个按插件规则校验，由用户决定装进哪些实例
	bundled := map[string][]FileEntry{}
	var order []string
	pluginsDirRel := ""
	for _, f := range inRoot {
		parts := strings.SplitN(f.Path, "/", 4)
		if len(parts) < 4 || !strings.EqualFold(parts[0], "ArkApi") || !strings.EqualFold(parts[1], "Plugins") {
			continue
		}
		pluginsDirRel = parts[0] + "/" + parts[1]
		x := parts[2]
		if _, ok := bundled[x]; !ok {
			order = append(order, x)
		}
		bundled[x] = append(bundled[x], FileEntry{Path: parts[3], Size: f.Size})
	}
	for _, x := range order {
		pr := &PluginReport{Files: []FileEntry{}, Errors: []string{}, Warnings: []string{}, Dependencies: []string{}}
		validatePluginDir(pr, filepath.Join(rep.root, filepath.FromSlash(pluginsDirRel), x), x, bundled[x])
		rep.Bundled = append(rep.Bundled, pr)
	}
	return rep
}

var fileNameVersion = regexp.MustCompile(`(\d+(?:\.\d+)+)`)

func versionFromFileName(name string) string {
	return fileNameVersion.FindString(filepath.Base(name))
}

// checkAMD64PE 确认 p 是 x64 的 PE 文件。先认 MZ 头：debug/pe 对没有 MZ 头的文件会
// 当成裸 COFF 解析，改了后缀的文本文件偶尔也能「通过」。
func checkAMD64PE(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()

	var mz [2]byte
	if _, err := io.ReadFull(f, mz[:]); err != nil || mz != [2]byte{'M', 'Z'} {
		return fmt.Errorf("不是 PE 文件")
	}
	pf, err := pe.NewFile(f)
	if err != nil {
		return err
	}
	if pf.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("不是 x64 程序（Machine=%#x）", pf.Machine)
	}
	return nil
}

// compareAPIVersions 比较 ArkApi 的 API 版本号。它们是**十进制小数**，不是点分版本：
// PluginInfo.json 的 MinApiVersion 是 JSON 数字（1.19、2），主程序是 2.03，
// 所以 2.1 表示 2.10、高于 2.03——按点分段比会得出相反的结论。两边都不是数字时退回点分比较。
func compareAPIVersions(a, b string) int {
	x, errX := strconv.ParseFloat(strings.TrimSpace(a), 64)
	y, errY := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if errX == nil && errY == nil {
		switch {
		case x > y:
			return 1
		case x < y:
			return -1
		}
		return 0
	}
	return compareVersions(a, b)
}

// compareVersions 按点分段比较插件版本号：数字段按数值（1.10 > 1.9），否则按字符串；
// 段数不同时缺的段视为 0。只用于「重装或降级」的提示，判断错了也不阻断。
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		x, y := "0", "0"
		if i < len(as) {
			x = strings.TrimSpace(as[i])
		}
		if i < len(bs) {
			y = strings.TrimSpace(bs[i])
		}
		xi, errX := strconv.Atoi(x)
		yi, errY := strconv.Atoi(y)
		if errX == nil && errY == nil {
			if c := xi - yi; c != 0 {
				return sign(c)
			}
			continue
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

func fileEntries(entries []archive.Entry) []FileEntry {
	var out []FileEntry
	for _, e := range entries {
		if !e.IsDir {
			out = append(out, FileEntry{Path: e.Path, Size: e.Size})
		}
	}
	return out
}

// splitUnder 把文件分成「在 rootRel 之下」（路径改为相对 rootRel）与「之外」两组。
func splitUnder(files []FileEntry, rootRel string) (in []FileEntry, out []string) {
	in = []FileEntry{}
	for _, f := range files {
		if rootRel == "." {
			in = append(in, f)
			continue
		}
		if rest, ok := strings.CutPrefix(f.Path, rootRel+"/"); ok {
			in = append(in, FileEntry{Path: rest, Size: f.Size})
		} else {
			out = append(out, f.Path)
		}
	}
	return in, out
}

// findFold 在 files 里找路径等于 p 的文件：精确匹配优先，其次不区分大小写。返回实际写法。
func findFold(files []FileEntry, p string) (string, bool) {
	if i := slices.IndexFunc(files, func(f FileEntry) bool { return f.Path == p }); i >= 0 {
		return p, true
	}
	if i := slices.IndexFunc(files, func(f FileEntry) bool { return strings.EqualFold(f.Path, p) }); i >= 0 {
		return files[i].Path, true
	}
	return "", false
}

func (r *PluginReport) addError(msg string)   { r.Errors = append(r.Errors, msg) }
func (r *PluginReport) addWarning(msg string) { r.Warnings = append(r.Warnings, msg) }
