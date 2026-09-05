package arkcache

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
	"time"
)

type exeHashEntry struct {
	modTime time.Time
	size    int64
	hash    string
}

// exeHashCache key: exe 路径 -> exeHashEntry
var exeHashCache sync.Map

// ExeHash 返回 ArkAscendedServer.exe 的 SHA256（小写十六进制），也就是 CDN 上
// 那个 ZIP 的文件名。
//
// ArkApi 算的是**它自己所在目录**的那份 exe（镜像里的副本），而镜像里的 exe 是源
// 文件的字节副本（同步靠 MD5 保证），两者哈希必然相同 —— 所以只算源的那份，一台
// 机器一次，全实例共用。
//
// SHA256 一个几百 MB 的文件在 SSD 上是亚秒级，但每次启动都算仍然浪费：按
// modTime+size 做内存缓存，服务器更新后自动失效。缓存写在公开函数内部，不另包一层
// 包装 —— 与 internal/instance 的 GetInstanceAsaVersion 是同一个写法。
//
// 一律 os.Open 只读打开：这个文件常常正被运行中的服务器占用，带写意图打开会失败。
func ExeHash(path string) (string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if v, ok := exeHashCache.Load(path); ok {
		entry := v.(exeHashEntry)
		if entry.modTime.Equal(stat.ModTime()) && entry.size == stat.Size() {
			return entry.hash, nil
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))

	exeHashCache.Store(path, exeHashEntry{modTime: stat.ModTime(), size: stat.Size(), hash: sum})
	return sum, nil
}
