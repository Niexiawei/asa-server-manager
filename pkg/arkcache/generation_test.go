package arkcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const testHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var generationNamePattern = regexp.MustCompile(`^[0-9a-f]{64}-\d+-\d+-\d+$`)

func TestNewGenerationNameShape(t *testing.T) {
	name := newGenerationName(testHash, 0)
	if !generationNamePattern.MatchString(name) {
		t.Fatalf("generation 名字不合形状: %s", name)
	}
	if !isSafeGenerationDirectory(generationRelPath(name)) {
		t.Fatalf("自己生成的名字没通过 IsSafeGenerationDirectory: %s", name)
	}
}

func TestIsSafeGenerationDirectory(t *testing.T) {
	ok := generationRelPath(newGenerationName(testHash, 0))

	for _, tc := range []struct {
		name string
		rel  string
		want bool
	}{
		{"正常", ok, true},
		{"反斜杠也认", strings.ReplaceAll(ok, "/", `\`), true},
		{"父目录不是 generations", "gens/" + testHash + "-1-2-0", false},
		{"没有父目录", testHash + "-1-2-0", false},
		{"多一层父目录", "a/generations/" + testHash + "-1-2-0", false},
		{"第一个连字符不在下标 64", "generations/" + testHash[:32] + "-1-2-0", false},
		{"连字符只有 2 个", "generations/" + testHash + "-1-2", false},
		{"连字符有 4 个", "generations/" + testHash + "-1-2-0-9", false},
		{"末段不是数字", "generations/" + testHash + "-1-2-x", false},
		{"末段为空", "generations/" + testHash + "-1-2-", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSafeGenerationDirectory(tc.rel); got != tc.want {
				t.Fatalf("isSafeGenerationDirectory(%q) = %v, want %v", tc.rel, got, tc.want)
			}
		})
	}
}

// seedGeneration 造一份完整可用的缓存，返回 cacheRoot 与 generation 相对路径。
func seedGeneration(t *testing.T, cacheRoot, hash, lastModified string) string {
	t.Helper()
	name := newGenerationName(hash, 0)
	rel := generationRelPath(name)
	dir := filepath.Join(cacheRoot, generationsRel, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, offsetsFileName), buildRecords(t, offsetValueSize, "k"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, bitfieldsFileName), buildRecords(t, bitfieldValueSize, "b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeMetadata(cacheRoot, metadata{
		Version:        cacheMetadataVersion,
		ExecutableHash: hash,
		LastModified:   lastModified,
		CacheDirectory: rel,
	}); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestInspect(t *testing.T) {
	root := t.TempDir()
	rel := seedGeneration(t, root, testHash, "Thu, 03 Sep 2026 15:20:10 GMT")

	res, err := Inspect(root, testHash)
	if err != nil || !res.Ready {
		t.Fatalf("应当有效: err=%v reason=%s", err, res.Reason)
	}
	if res.Generation != rel {
		t.Fatalf("Generation = %q, want %q", res.Generation, rel)
	}
	if res.LastModified != "Thu, 03 Sep 2026 15:20:10 GMT" {
		t.Fatalf("LastModified 没读回来: %q", res.LastModified)
	}
	if res.From != FromExisting {
		t.Fatalf("From = %q", res.From)
	}

	// 哈希不匹配（ARK 更新后的形态）
	other := strings.Repeat("a", 64)
	if res, err := Inspect(root, other); err == nil || res.Ready {
		t.Fatal("哈希不匹配时应当判无效")
	}

	// generation 里少一个文件
	os.Remove(filepath.Join(root, filepath.FromSlash(rel), bitfieldsFileName))
	if res, err := Inspect(root, testHash); err == nil || res.Ready {
		t.Fatal("缺 cached_bitfields.cache 时应当判无效")
	}
}

// ArkApi 自己留下的历史格式：整个 cached_key.cache 就是一个裸哈希，
// 缓存文件直接落在 Cache 根。认不出来就会把一份合法缓存误判为无效再下一遍。
func TestInspectBareHashLegacyFormat(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, offsetsFileName), buildRecords(t, offsetValueSize, "k"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, bitfieldsFileName), buildRecords(t, bitfieldValueSize, "b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, metadataFileName), []byte(strings.ToUpper(testHash)), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Inspect(root, testHash)
	if err != nil || !res.Ready {
		t.Fatalf("裸哈希格式应当被认作有效: err=%v reason=%s", err, res.Reason)
	}
	if res.Generation != "" {
		t.Fatalf("裸哈希格式的 Generation 应为空，得到 %q", res.Generation)
	}
}

func TestParseMetadataRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"空", "  "},
		{"不是 JSON", "not json"},
		{"version 不是 1", `{"version":2,"executable_hash":"` + testHash + `"}`},
		{"哈希长度不对", `{"version":1,"executable_hash":"abc"}`},
		{"cache_directory 不安全", `{"version":1,"executable_hash":"` + testHash + `","cache_directory":"../evil"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseMetadata([]byte(tc.body)); err == nil {
				t.Fatal("应当报错")
			}
		})
	}
}

func TestWriteMetadataIsAtomicAndComplete(t *testing.T) {
	root := t.TempDir()
	rel := seedGeneration(t, root, testHash, "LM")

	data, err := os.ReadFile(filepath.Join(root, metadataFileName))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "executable_hash", "last_modified", "cache_directory"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("cached_key.cache 缺字段 %s", k)
		}
	}
	if raw["cache_directory"] != rel {
		t.Fatalf("cache_directory = %v, want %q（必须带 generations/ 前缀）", raw["cache_directory"], rel)
	}
	if left, _ := filepath.Glob(filepath.Join(root, metadataFileName+".*.tmp")); len(left) != 0 {
		t.Fatalf(".tmp 没有被清掉: %v", left)
	}
}

func TestPruneGenerationsKeepsCurrentHash(t *testing.T) {
	root := t.TempDir()
	current := seedGeneration(t, root, testHash, "LM")

	otherHash := strings.Repeat("b", 64)
	stale := generationRelPath(newGenerationName(otherHash, 0))
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(stale)), 0o755); err != nil {
		t.Fatal(err)
	}

	removed := pruneGenerations(root, testHash, current, 0, true)
	if len(removed) != 1 || removed[0] != stale {
		t.Fatalf("预演结果 = %v，want [%s]", removed, stale)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(stale))); err != nil {
		t.Fatal("dryRun 不应真删")
	}

	pruneGenerations(root, testHash, current, 0, false)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(stale))); err == nil {
		t.Fatal("非当前哈希的旧代应当被删")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(current))); err != nil {
		t.Fatal("当前 generation 被误删了")
	}
}

// 跨哈希交错（另一个进程刚为 h2 提交了 metadata）时，以 h1 的身份清理也不能删掉
// metadata 此刻指向的 h2 那一代。
func TestPruneGenerationsKeepsReferencedGeneration(t *testing.T) {
	root := t.TempDir()
	h1 := seedGeneration(t, root, testHash, "LM")
	h2 := seedGeneration(t, root, strings.Repeat("b", 64), "LM") // metadata 现在指向它

	pruneGenerations(root, testHash, h1, 0, false)
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(h2))); err != nil {
		t.Fatal("metadata 正指向的 generation 被删了")
	}
	if res, err := Inspect(root, ""); err != nil || !res.Ready {
		t.Fatalf("清理之后缓存不可用: %v %s", err, res.Reason)
	}
}

func TestStagingIsNotAGeneration(t *testing.T) {
	root := t.TempDir()
	current := seedGeneration(t, root, testHash, "LM")

	fresh := filepath.Join(root, generationsRel, stagingPrefix+newGenerationName(testHash, 1))
	stale := filepath.Join(root, generationsRel, stagingPrefix+newGenerationName(testHash, 2))
	for _, d := range []string{fresh, stale} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleStagingAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	for _, name := range listGenerations(root) {
		if strings.HasPrefix(name, stagingPrefix) {
			t.Fatalf("listGenerations 列出了 staging: %s", name)
		}
	}
	pruneGenerations(root, testHash, current, 0, false)
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("别人正在解压的 staging 被删了")
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("崩溃遗留的 staging 没有被回收")
	}
}

func TestWriteMetadataConcurrentWritersNeverTear(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := strings.Repeat(string(rune('a'+i%6)), 64)
			for j := 0; j < 20; j++ {
				_ = writeMetadata(root, metadata{Version: cacheMetadataVersion, ExecutableHash: h,
					LastModified: strings.Repeat("x", 200*i), CacheDirectory: generationRelPath(newGenerationName(h, j))})
			}
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(root, metadataFileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseMetadata(data); err != nil {
		t.Fatalf("并发写之后 metadata 不合法: %v", err)
	}
}
