package arkcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// metadataFileName 是 Cache 根下的指针文件（ArkBaseApi.cpp:272-276）。
	metadataFileName = "cached_key.cache"
	// generationsRel 是 generation 目录的父目录名。IsSafeGenerationDirectory
	// （ArkBaseApi.cpp:81-113）要求父目录**恰好**是这个名字。
	generationsRel = "generations"
	// stagingPrefix 是解压中的 generation 的目录名前缀：generations/.staging-<name>。
	// 校验通过后整目录 rename 成 <name>。不是合法 generation 名，ArkApi 不会认它；
	// 镜像同步跳过它（StagingDirPrefix）。
	stagingPrefix = ".staging-"
	// staleStagingAge 之后的 staging 目录视为崩溃遗留，由清理回收。一次解压是
	// 秒级的，留这么宽是为了不碰别的进程正在解压的那一个。
	staleStagingAge = time.Hour

	offsetsFileName   = "cached_offsets.cache"
	bitfieldsFileName = "cached_bitfields.cache"
	// offsetsTxtFileName 是 saveToFilePlain 产出的排序键名清单（Cache.cpp:155），
	// 纯人读调试用。ArkApi 会把它落进 generation 但从不读 —— 我们不提取。
	offsetsTxtFileName = "cached_offsets.txt"

	// cacheMetadataVersion 必须等于 1（ParseCacheMetadata:115-146）。
	cacheMetadataVersion = 1
)

// metadata 是 cached_key.cache 的内容。字段名逐字对着 C++ 的 nlohmann::json 写。
type metadata struct {
	Version        int    `json:"version"`
	ExecutableHash string `json:"executable_hash"`
	LastModified   string `json:"last_modified"`
	CacheDirectory string `json:"cache_directory"`
}

// parseMetadata 复刻 ParseCacheMetadata（ArkBaseApi.cpp:115-146）。
//
// 除了 JSON 形态，它还接受**裸 64 位哈希**作为整个文件内容（历史格式，:141-143），
// 此时 cache_directory 为空、缓存文件直接落在 Cache 根。我们不产生这种形态，但
// Inspect 必须认得它 —— 否则会把一份 ArkApi 自己留下的合法缓存误判为无效，
// 转头再下一遍。
func parseMetadata(data []byte) (metadata, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return metadata{}, fmt.Errorf("cached_key.cache 为空")
	}

	if isHex64(trimmed) {
		return metadata{Version: cacheMetadataVersion, ExecutableHash: strings.ToLower(trimmed)}, nil
	}

	var m metadata
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return metadata{}, fmt.Errorf("cached_key.cache 不是合法 JSON: %w", err)
	}
	if m.Version != cacheMetadataVersion {
		return metadata{}, fmt.Errorf("cached_key.cache version=%d，只认 %d", m.Version, cacheMetadataVersion)
	}
	if !isHex64(m.ExecutableHash) {
		return metadata{}, fmt.Errorf("cached_key.cache executable_hash 不是 64 位十六进制")
	}
	m.ExecutableHash = strings.ToLower(m.ExecutableHash)
	if m.CacheDirectory != "" && !isSafeGenerationDirectory(m.CacheDirectory) {
		return metadata{}, fmt.Errorf("cached_key.cache cache_directory %q 不是合法 generation 路径", m.CacheDirectory)
	}
	return m, nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

// isSafeGenerationDirectory 复刻 IsSafeGenerationDirectory（ArkBaseApi.cpp:81-113）：
// 父目录必须**恰好**是 generations；目录名里第一个连字符必须正好在下标 64，
// 总共 3 个连字符，其后三段必须是非空纯数字。
func isSafeGenerationDirectory(rel string) bool {
	clean := path.Clean(strings.ReplaceAll(rel, "\\", "/"))
	dir, name := path.Split(clean)
	if strings.Trim(dir, "/") != generationsRel {
		return false
	}
	if strings.Index(name, "-") != 64 {
		return false
	}
	parts := strings.Split(name, "-")
	if len(parts) != 4 { // 恰好 3 个连字符
		return false
	}
	for _, p := range parts[1:] {
		if p == "" || !allDigits(p) {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// newGenerationName 生成 hash-pid-UnixMilli-suffix。
// C++ 用的是 GetTickCount64()，但它只校验格式（三段非空纯数字），不要求数字
// 来自哪个 API —— UnixMilli 同样合法，而且跨平台。
func newGenerationName(hash string, suffix int) string {
	return fmt.Sprintf("%s-%d-%d-%d", hash, os.Getpid(), time.Now().UnixMilli(), suffix)
}

// generationRelPath 是写进 metadata 的 cache_directory：**相对 Cache 根**的
// "generations/<name>"。写成裸的 "<name>" 会让 C++ 去 Cache 根下找，那不是
// generation 目录（参考文档 §7）。
func generationRelPath(name string) string { return generationsRel + "/" + name }

// Inspect 判断 cacheRoot 下现有的缓存对 exeHash 是否可用。
//
// 这是**快路径**：只做「metadata 字段对得上 + 两个 .cache 存在且非空」，不重复跑
// validateSerializedMap —— 那份文件是我们自己写完校验过的，且 ArkApi 启动时还会
// 再验一遍（方案 §12.3）。exeHash 为空表示不比对哈希，只看结构。
func Inspect(cacheRoot, exeHash string) (Result, error) {
	res := Result{Hash: strings.ToLower(exeHash)}

	data, err := os.ReadFile(filepath.Join(cacheRoot, metadataFileName))
	if err != nil {
		res.Reason = "没有 cached_key.cache"
		return res, err
	}
	m, err := parseMetadata(data)
	if err != nil {
		res.Reason = err.Error()
		return res, err
	}
	if res.Hash != "" && m.ExecutableHash != res.Hash {
		res.Reason = fmt.Sprintf("缓存属于 exe %s..，当前 exe 是 %s..", short(m.ExecutableHash), short(res.Hash))
		return res, errors.New(res.Reason)
	}

	genDir := cacheRoot
	if m.CacheDirectory != "" {
		genDir = filepath.Join(cacheRoot, filepath.FromSlash(m.CacheDirectory))
	}
	for _, name := range []string{offsetsFileName, bitfieldsFileName} {
		fi, statErr := os.Stat(filepath.Join(genDir, name))
		if statErr != nil {
			res.Reason = fmt.Sprintf("缺少 %s", name)
			return res, statErr
		}
		if !fi.Mode().IsRegular() || fi.Size() == 0 {
			res.Reason = fmt.Sprintf("%s 不是常规文件或为空", name)
			return res, errors.New(res.Reason)
		}
	}

	res.Ready = true
	res.Hash = m.ExecutableHash
	res.Generation = m.CacheDirectory
	res.LastModified = m.LastModified
	res.From = FromExisting
	return res, nil
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// StagingDirPrefix 供镜像同步识别 generations/ 下解压中的目录：它们永远不该被
// 复制进实例。
const StagingDirPrefix = stagingPrefix

// writeMetadata 原子写 cached_key.cache：唯一名的 .tmp + Rename。
//
// C++ 侧的 saveToFile（Cache.cpp:69）是 .tmp + FlushFileBuffers +
// MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)；Go 的 os.Rename 在 Windows 上映射为
// MoveFileEx(REPLACE_EXISTING)，语义一致。
//
// 这一步必须是**最后一步**：generation 完整、两个 .cache 都通过结构校验之后才写。
// 顺序颠倒会留下「metadata 指向一个不存在/不完整的 generation」——那是 C++ 侧最难
// 诊断的形态。
func writeMetadata(cacheRoot string, m metadata) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return err
	}
	final := filepath.Join(cacheRoot, metadataFileName)

	// 临时名每次唯一：两个写者（commitMu 管不到的另一个进程）共用一个 .tmp 会
	// 互相截断，rename 过去的可能是半份 JSON。
	f, err := os.CreateTemp(cacheRoot, metadataFileName+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Chmod(0o644); err != nil { // CreateTemp 建的是 0600
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// listGenerations 返回 cacheRoot/generations 下的目录名，按名字升序
// （名字里第三段是 UnixMilli，所以同一哈希下升序即时间序）。解压中的 staging
// 目录不算 generation。
func listGenerations(cacheRoot string) []string {
	entries, err := os.ReadDir(filepath.Join(cacheRoot, generationsRel))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), stagingPrefix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// referencedGeneration 现读 cached_key.cache，返回它此刻指向的 generation 目录名；
// 读不出来时返回空串。
func referencedGeneration(cacheRoot string) string {
	data, err := os.ReadFile(filepath.Join(cacheRoot, metadataFileName))
	if err != nil {
		return ""
	}
	m, err := parseMetadata(data)
	if err != nil || m.CacheDirectory == "" {
		return ""
	}
	return path.Base(m.CacheDirectory)
}

// pruneGenerations 删除 generations/ 下不该留的目录，返回被删（dryRun 时是将被删）
// 的相对路径。调用方持 commitMu。
//
// 规则：keepRel 指向的那一代与 cached_key.cache **此刻**指向的那一代永远保留；
// 其余同哈希的按名字升序额外保留最新的 keep 代；非当前哈希的一律删。后一条保护
// 是给跨进程用的：另一个进程刚为另一个哈希提交了 metadata，那一代就不能因为
// 「不是我的哈希」被删掉。超过 staleStagingAge 的 staging 目录一并回收。
//
// 注意这只对**源目录**有意义：镜像里的旧代由 CleanupOldCacheGenerations
// （ArkBaseApi.cpp:210-232）在 ArkApi 每次启动时整棵删掉，留几代由不得我们。
func pruneGenerations(cacheRoot, hash, keepRel string, keep int, dryRun bool) []string {
	keepName := path.Base(keepRel)
	referenced := referencedGeneration(cacheRoot)
	hash = strings.ToLower(hash)
	var (
		sameHash []string
		removed  []string
	)
	for _, name := range listGenerations(cacheRoot) {
		if name == keepName || name == referenced {
			continue
		}
		if hash != "" && strings.HasPrefix(strings.ToLower(name), hash+"-") {
			sameHash = append(sameHash, name)
			continue
		}
		removed = append(removed, removeGeneration(cacheRoot, name, dryRun)...)
	}
	for i := 0; i < len(sameHash)-keep; i++ {
		removed = append(removed, removeGeneration(cacheRoot, sameHash[i], dryRun)...)
	}
	return append(removed, pruneStaleStaging(cacheRoot, dryRun)...)
}

// pruneStaleStaging 回收崩溃遗留的 staging 目录。
func pruneStaleStaging(cacheRoot string, dryRun bool) []string {
	entries, err := os.ReadDir(filepath.Join(cacheRoot, generationsRel))
	if err != nil {
		return nil
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), stagingPrefix) {
			continue
		}
		fi, err := e.Info()
		if err != nil || time.Since(fi.ModTime()) < staleStagingAge {
			continue
		}
		removed = append(removed, removeGeneration(cacheRoot, e.Name(), dryRun)...)
	}
	return removed
}

func removeGeneration(cacheRoot, name string, dryRun bool) []string {
	rel := generationRelPath(name)
	if dryRun {
		return []string{rel}
	}
	if err := os.RemoveAll(filepath.Join(cacheRoot, generationsRel, name)); err != nil {
		return nil
	}
	return []string{rel}
}
