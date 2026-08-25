package plugindata

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// sqliteMagic 是 SQLite 数据库文件头的前 16 字节（含结尾 NUL）。
// 见 https://www.sqlite.org/fileformat.html#the_database_header
var sqliteMagic = []byte("SQLite format 3\x00")

// companionSuffixes 是 SQLite 主库之外的伴随文件后缀。
// -wal/-shm 是 WAL 模式的写前日志与共享内存索引，-journal 是回滚日志模式的。
// 它们随时可能被 SQLite 删除重建，**必须与主库作为一个整体搬运**：
// 只搬主库会丢掉尚未 checkpoint 的数据（实测 ArkDB.db 只有 4 KB，
// 而 ArkDB.db-wal 有 1.9 MB）。
var companionSuffixes = []string{"-wal", "-shm", "-journal"}

// extraDataFiles 允许按插件名补充「既不叫 config.json、也认不出是 SQLite、
// 名字又不像数据库」的运行期数据文件。键是插件目录名，值是相对插件目录的文件名。
//
// 目前为空：已知插件的数据文件都能被自动规则认出来。留这个口子是因为
// 插件生态不受我们控制，出现反例时不必改判定逻辑。
var extraDataFiles = map[string][]string{}

// fileGroup 是一组必须整体搬运的文件。
//
// 对 SQLite 来说组 = 主库 + 伴随文件；对配置和其他数据文件来说组只有一个成员。
// Base 是组的主文件相对路径（相对插件目录，forward slash）；
// Members 是**磁盘上实际存在**的成员，可能不含 Base（例如只剩下一个孤立的 -wal）。
type fileGroup struct {
	Base     string
	Members  []string
	IsConfig bool
	IsSQLite bool
}

// allMemberPaths 返回该组在任意一侧可能占用的全部相对路径 —— 用于**先删后拷**。
//
// 不能只删 Members：目标侧可能有源侧没有的伴随文件（比如源已 checkpoint 掉了 -wal，
// 目标侧还留着旧的），逐文件覆盖会拼出「新主库 + 旧 WAL」这种互不匹配的组合，
// SQLite 打开时会拿旧 WAL 去重放，比不搬还糟。
func (g fileGroup) allMemberPaths() []string {
	if g.IsConfig || len(g.Members) == 0 && g.Base == "" {
		return g.Members
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(g.Base)
	for _, suffix := range companionSuffixes {
		add(g.Base + suffix)
	}
	for _, m := range g.Members {
		add(m)
	}
	return out
}

// isSQLiteFile 按文件头（而非扩展名）判断是否是 SQLite 数据库。
//
// 用魔数是刻意的：插件把库命名成 .dat、.bin 都有可能，按扩展名判定会漏，
// 而漏掉的后果是这个库既不被搬运也不被快照，静默丢数据。
func isSQLiteFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	head := make([]byte, len(sqliteMagic))
	n, err := f.Read(head)
	if err != nil || n < len(sqliteMagic) {
		return false
	}
	return bytes.Equal(head, sqliteMagic)
}

// looksLikeDataFile 是扩展名层面的兜底判定，用于认出**当前为空**（因而没有魔数）
// 或尚未创建的数据库文件。真正的识别以 isSQLiteFile 为准。
func looksLikeDataFile(name string) bool {
	l := strings.ToLower(name)
	for _, ext := range []string{".db", ".sqlite", ".sqlite3"} {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	// ArkDB.db-wal 这类：主库名 + 伴随后缀
	for _, suffix := range companionSuffixes {
		if strings.HasSuffix(l, suffix) {
			return looksLikeDataFile(strings.TrimSuffix(l, suffix))
		}
	}
	return false
}

// groupKey 把伴随文件归到主库名下。ArkDB.db-wal -> ArkDB.db
func groupKey(relPath string) string {
	for _, suffix := range companionSuffixes {
		if strings.HasSuffix(relPath, suffix) {
			return strings.TrimSuffix(relPath, suffix)
		}
	}
	return relPath
}

// scanPluginDir 扫描一个插件目录，返回需要搬运的文件组。
//
// root 不存在时返回空切片而非错误 —— 调用方遍历的是「镜像里有哪些插件」，
// 实例侧那一半很可能还没建起来。
func scanPluginDir(root, pluginName string) []fileGroup {
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil
	}

	extras := map[string]bool{}
	for _, name := range extraDataFiles[pluginName] {
		extras[filepath.ToSlash(name)] = true
	}

	// 先收集所有普通文件的相对路径
	var files []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 读不到的条目跳过，不阻断整轮搬运
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			// snapshots/ 是我们自己写的在线快照，不参与搬运
			if rel == snapshotsDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, rel)
		return nil
	})

	// 按主库名聚合
	groups := map[string]*fileGroup{}
	var order []string
	for _, rel := range files {
		key := groupKey(rel)
		g, ok := groups[key]
		if !ok {
			g = &fileGroup{Base: key}
			groups[key] = g
			order = append(order, key)
		}
		g.Members = append(g.Members, rel)
	}

	var out []fileGroup
	for _, key := range order {
		g := groups[key]
		sort.Strings(g.Members)

		base := filepath.Base(key)
		switch {
		case base == configFileName && len(g.Members) == 1:
			g.IsConfig = true
		case isSQLiteFile(filepath.Join(root, filepath.FromSlash(key))):
			g.IsSQLite = true
		case looksLikeDataFile(base) || extras[key]:
			// 认得出是数据库但当前读不到魔数（空库 / 只剩伴随文件）
		default:
			continue // 二进制、pdb、说明文档等一律不搬
		}
		out = append(out, *g)
	}
	return out
}

// maxMTime 返回一组文件里最新的修改时间；一个都不存在时返回零值。
//
// 判定必须以**组内最新**为准：-wal 通常比主库新得多，只看主库 mtime
// 会把「刚写过一大批数据」误判成「没动过」。
func maxMTime(dir string, rels []string) (t time.Time, exists bool) {
	for _, rel := range rels {
		fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		exists = true
		if m := fi.ModTime(); m.After(t) {
			t = m
		}
	}
	return t, exists
}
