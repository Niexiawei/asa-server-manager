# Ark ASA Cache 外部 Go 下载器实施方案

> 目标：绕过 ArkServerAPI 当前 C++ 内置的 Cache 下载逻辑，由独立 Go 程序负责 Cache 下载、断点续传、解压、校验、生成 `cached_key.cache`。
>
> 依据：用户提供的完整 `ArkBaseApi.cpp`。本文中的目录、文件名、metadata 格式和 C++ 行为均以该源码为准。

---

## 1. 目标

当前 ArkServerAPI 在 `ArkBaseApi::Init()` 中会根据：

```text
SHA256(ArkAscendedServer.exe)
```

拼接：

```text
<hash>.zip
```

然后从 CDN 下载 Cache。

问题是：

- Cache ZIP 较大；
- 原下载失败后会重新开始；
- 网络不稳定时启动 ArkServer 容易卡在 Cache 获取阶段；
- 希望将下载职责从 C++ DLL 启动流程中剥离。

最终方案：

```text
Go Cache Downloader
    |
    +-- 计算 ArkAscendedServer.exe SHA256
    |
    +-- 判断本地 Cache 是否已经匹配
    |
    +-- HTTP Range 断点续传
    |
    +-- ZIP 下载到 .part
    |
    +-- 下载完成后原子提交
    |
    +-- 创建 generations/<hash>-<数字>-<数字>-<数字>
    |
    +-- 解压 cached_offsets.cache
    |       cached_bitfields.cache
    |
    +-- 校验文件
    |
    +-- 生成 cached_key.cache
    |
    +-- 删除 ZIP
    |
    +-- 启动 ArkServer
```

HTTP Range 的语义是客户端通过 `Range: bytes=<offset>-` 请求剩余字节，服务器正常支持时返回 `206 Partial Content`；服务器也可能忽略 Range 并返回 `200`，此时客户端不能把完整响应追加到已有 `.part` 文件中。Go 的 `net/http` 可以直接设置 Range Header。

---

# 2. C++ 当前 Cache 目录

源码：

```cpp
fs::path exe_path = std::filesystem::path(buffer).parent_path();
const fs::path executableFile = exe_path / "ArkAscendedServer.exe";
const fs::path arkApiDir = exe_path / ArkBaseApi::GetApiName();
const fs::path cacheRoot = arkApiDir / "Cache";
const fs::path keyCacheFile = cacheRoot / "cached_key.cache";
```

`GetApiName()` 返回：

```cpp
return "ArkApi";
```

因此最终目录是：

```text
<ServerDir>/
├── ArkAscendedServer.exe
└── ArkApi/
    └── Cache/
        ├── cached_key.cache
        └── generations/
            └── <generation>/
                ├── cached_offsets.cache
                └── cached_bitfields.cache
```

---

# 3. Cache ZIP 下载 URL

C++：

```cpp
const std::string archiveName = fileHash + ".zip";
```

其中：

```text
fileHash = SHA256(ArkAscendedServer.exe)
```

默认 CDN：

```text
https://cdn.pelayori.com/cache/
```

所以：

```text
https://cdn.pelayori.com/cache/<SHA256>.zip
```

例如：

```text
https://cdn.pelayori.com/cache/0123456789abcdef....abcdef.zip
```

备用 CDN：

```text
https://cdn.shadowhunter.co.za/cache/
https://cdn.shadowhunter-systems.co.za/cache/
```

---

# 4. C++ 对 generation 目录的严格要求

源码 `IsSafeGenerationDirectory()` 要求：

```text
generations/<64位SHA256>-<数字>-<数字>-<数字>
```

例如：

```text
generations/
└── 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef-12345-1756912345678-0/
```

其中：

```text
0123...cdef
    |
    +-- 必须正好 64 个十六进制字符

12345
    |
    +-- 数字

1756912345678
    |
    +-- 数字

0
    |
    +-- 数字
```

不要使用：

```text
generations/cache-xxx
```

也不要使用：

```text
generation/<hash>
```

必须是：

```text
generations/<hash>-<number>-<number>-<number>
```

---

# 5. generation 的命名

C++ 原始实现：

```cpp
const std::string generationPrefix =
    localFile.stem().string()
    + "-"
    + std::to_string(GetCurrentProcessId())
    + "-"
    + std::to_string(GetTickCount64());
```

最终：

```text
<hash>-<pid>-<tick>-<suffix>
```

Go 不需要完全复制 Windows 的 `GetTickCount64()`。

推荐：

```text
<hash>-<pid>-<UnixMilli>-<suffix>
```

例如：

```text
abcdef...1234-3210-1756912345678-0
```

C++ 只检查这些字段是不是合法格式，并不要求具体数字来自哪个 API。

---

# 6. cached_key.cache 格式

这是整个方案最关键的文件。

C++ 序列化：

```cpp
return nlohmann::json{
    { "version", cache_metadata_version },
    { "executable_hash", metadata.executableHash },
    { "last_modified", metadata.lastModified },
    { "cache_directory", metadata.cacheDirectory }
}.dump();
```

当前：

```text
cache_metadata_version = 1
```

因此：

```json
{
  "version": 1,
  "executable_hash": "<SHA256>",
  "last_modified": "<HTTP Last-Modified>",
  "cache_directory": "generations/<generation>"
}
```

例如：

```json
{
  "version": 1,
  "executable_hash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "last_modified": "Thu, 03 Sep 2026 15:20:10 GMT",
  "cache_directory": "generations/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef-1234-1756912345678-0"
}
```

---

# 7. `cache_directory` 必须是相对 Cache 根目录的路径

C++：

```cpp
state.cacheDirectory = cacheRoot;

if (!state.metadata->cacheDirectory.empty())
    state.cacheDirectory /= std::filesystem::path(
        state.metadata->cacheDirectory
    );
```

所以：

```json
"cache_directory": "generations/abc-1-2-0"
```

对应：

```text
ArkApi/Cache/generations/abc-1-2-0/
```

不要写：

```json
"cache_directory": "abc-1-2-0"
```

因为 C++ 会变成：

```text
ArkApi/Cache/abc-1-2-0/
```

这不是当前 generation 目录。

---

# 8. Cache 必须包含的文件

启动时 C++ 最终读取：

```text
cached_offsets.cache
cached_bitfields.cache
```

代码：

```cpp
state.offsetsFile =
    state.cacheDirectory / "cached_offsets.cache";

state.bitfieldsFile =
    state.cacheDirectory / "cached_bitfields.cache";
```

并执行：

```cpp
Cache::validateSerializedMap<intptr_t>(
    state.offsetsFile
);

Cache::validateSerializedMap<BitField>(
    state.bitfieldsFile
);
```

所以 Go 至少必须保证：

```text
generations/<generation>/
├── cached_offsets.cache
└── cached_bitfields.cache
```

---

# 9. ZIP 内允许的文件

C++ `DownloadCacheFiles()` 明确允许：

```text
cached_offsets.cache
cached_bitfields.cache
cached_key.cache
cached_offsets.txt
```

其他文件直接拒绝。

因此 ZIP 推荐结构：

```text
cache.zip
├── cached_offsets.cache
└── cached_bitfields.cache
```

如果 CDN 原始 ZIP 还带：

```text
cached_key.cache
cached_offsets.txt
```

当前 C++ 也允许。

但是对于 Go 外部下载器，建议只提取：

```text
cached_offsets.cache
cached_bitfields.cache
```

然后自行生成新的：

```text
cached_key.cache
```

---

# 10. 不要把 ZIP 中的 cached_key.cache 直接复制

推荐：

```text
ZIP
 |
 +-- cached_offsets.cache
 |
 +-- cached_bitfields.cache
 |
 +-- cached_key.cache   <-- 忽略
 |
 +-- cached_offsets.txt <-- 可忽略
```

然后 Go 根据当前：

```text
ArkAscendedServer.exe
```

的 SHA256 自己生成：

```text
ArkApi/Cache/cached_key.cache
```

原因是 metadata 必须对应当前 EXE。

---

# 11. 完整下载流程

推荐：

```text
开始
 |
 v
找到 ArkAscendedServer.exe
 |
 v
计算 SHA256
 |
 v
读取 cached_key.cache
 |
 +---- executable_hash 相同
 |              |
 |              v
 |       generation 存在
 |              |
 |              v
 |       offsets/bitfields 存在
 |              |
 |              v
 |          不需要下载
 |
 +---- 不匹配
              |
              v
       下载 <hash>.zip
              |
              v
       <hash>.zip.part
              |
              v
       HTTP Range 续传
              |
              v
       下载完整
              |
              v
       rename .part -> .zip
              |
              v
       创建 generation
              |
              v
       解压
              |
              v
       校验两个 cache
              |
              v
       写 cached_key.cache
              |
              v
       删除旧 generation
              |
              v
             完成
```

---

# 12. `.part` 文件设计

不要直接：

```text
Cache/<hash>.zip
```

边下载边写。

应该：

```text
Cache/<hash>.zip.part
```

下载成功后：

```text
<hash>.zip.part
        |
        | 下载完成
        v
<hash>.zip
```

如果网络中断：

```text
<hash>.zip.part
```

必须保留。

下一次启动：

```text
stat(part)
    |
    v
size = N
    |
    v
Range: bytes=N-
```

继续下载。

---

# 13. Range 断点续传

核心请求：

```http
GET /cache/<hash>.zip HTTP/1.1
Range: bytes=335544320-
```

如果服务器支持：

```http
HTTP/1.1 206 Partial Content
Content-Range: bytes 335544320-629145599/629145600
```

那么：

```text
本地 .part
0 ---------------- 335MB
                    +
服务器
335MB ------------- 600MB
                    =
完整 ZIP
```

如果服务器返回：

```http
HTTP/1.1 200 OK
```

而不是：

```http
206 Partial Content
```

说明 Range 没有被服务端实际采用。

此时必须：

```text
删除/截断 .part
重新完整下载
```

不能 append。

---

# 14. HTTP Client 建议

不要给整个大文件设置：

```go
Timeout: 30 * time.Second
```

否则即使下载正在正常进行，只要整体时间超过 30 秒也会被取消。

建议：

```go
client := &http.Client{
    Timeout: 0,
}
```

然后通过：

```text
连接超时
TLS handshake 超时
读取超时
```

分别控制。

大文件下载最重要的是：

```text
单次网络连接失败
        ↓
保留 .part
        ↓
重新 GET
        ↓
Range 继续
```

---

# 15. 下载重试策略

建议：

```text
最大重试：无限 / 配置
首次失败：1 秒
第二次：2 秒
第三次：4 秒
第四次：8 秒
...
最大：30 秒
```

例如：

```text
下载 300 MB
    ↓
连接断开
    ↓
保存 300 MB .part
    ↓
等待 1 秒
    ↓
Range bytes=314572800-
    ↓
继续
```

这样比重新下载 0 MB 稳定很多。

---

# 16. 下载 URL fallback

建议 Go 支持：

```text
1. DownloadCacheURL
2. cdn.pelayori.com
3. cdn.shadowhunter.co.za
4. cdn.shadowhunter-systems.co.za
```

例如：

```text
https://cdn.pelayori.com/cache/<hash>.zip
https://cdn.shadowhunter.co.za/cache/<hash>.zip
https://cdn.shadowhunter-systems.co.za/cache/<hash>.zip
```

特别注意：

如果 `.part` 已经有 300 MB：

```text
CDN A 失败
    ↓
CDN B
    ↓
Range bytes=300MB-
```

但切换 CDN 前最好确认：

```text
ETag
Last-Modified
Content-Length
```

对应的是同一个资源。

最简单的安全方案：

```text
下载前 HEAD
获取 Content-Length
获取 ETag
获取 Last-Modified

保存到旁车元数据
```

如果切换 CDN 后：

```text
Content-Length 不一致
```

不要继续 append，应该重新下载。

---

# 17. Last-Modified

C++ 原逻辑会检查：

```cpp
Requests::GetFileLastModified(...)
```

然后：

```cpp
localCache.metadata->lastModified == remoteTimestamp
```

相同：

```text
本地 Cache current
```

不同：

```text
重新下载
```

但是因为你准备：

```json
"AutomaticCacheDownload": {
    "Enable": false
}
```

所以 C++ 不会再访问 CDN。

因此 Go 自己负责：

```text
Last-Modified
```

即可。

---

# 18. Go 是否必须生成 Last-Modified？

推荐生成。

```json
{
  "version": 1,
  "executable_hash": "...",
  "last_modified": "Thu, 03 Sep 2026 15:20:10 GMT",
  "cache_directory": "generations/..."
}
```

如果 HEAD 获取不到：

```json
"last_modified": ""
```

也是可以的。

因为 C++ 本地读取只验证：

```text
executable_hash
cache_directory
两个 cache 文件
```

---

# 19. Go 判断本地 Cache 是否可用

Go 启动时：

```text
SHA256(ArkAscendedServer.exe)
        |
        v
cached_key.cache
        |
        +-- executable_hash == SHA256 ?
        |
        +-- cache_directory 是否存在？
        |
        +-- cached_offsets.cache 是否存在？
        |
        +-- cached_bitfields.cache 是否存在？
```

全部满足：

```text
直接完成
```

否则：

```text
下载新的 Cache
```

---

# 20. C++ 的最终启动行为

配置：

```json
{
  "settings": {
    "AutomaticCacheDownload": {
      "Enable": false
    }
  }
}
```

之后 C++ 不再调用：

```cpp
DownloadCacheFiles(...)
```

而是：

```cpp
selectedCache =
    InspectLocalCache(
        cacheRoot,
        keyCacheFile,
        fileHash
    );
```

如果有效：

```text
读取 cached_offsets.cache
读取 cached_bitfields.cache
Offsets::Init()
AsaApi::InitHooks()
```

---

# 21. C++ 启动时不会重新下载

关闭：

```json
"Enable": false
```

后，C++ 的逻辑是：

```text
InspectLocalCache
       |
       +-- usable
       |      |
       |      v
       |   使用 Cache
       |
       +-- unusable
              |
              v
       每隔 30~60 秒重新检查本地
```

因此：

**Go 工具必须在启动 ArkServer 前确保 Cache 已准备好。**

推荐：

```text
Go Downloader
     |
     v
Cache Ready
     |
     v
启动 ArkAscendedServer.exe
```

不要：

```text
启动 ArkServer
     |
     +-- Go 同时下载 Cache
```

否则 C++ 可能在 Cache 不存在时进入等待。

---

# 22. 原子提交 cached_key.cache

不要直接：

```go
os.WriteFile(
    "cached_key.cache",
    data,
    0644,
)
```

推荐：

```text
cached_key.cache.tmp
        |
        | 完整写入
        v
rename
        |
        v
cached_key.cache
```

这样即使 Go 在写 metadata 时异常退出，也不会留下半截 JSON。

---

# 23. 正确的提交顺序

这是非常重要的。

推荐：

```text
1. 下载 ZIP 到 .part
2. 下载完成
3. .part -> .zip
4. 创建 generation
5. 解压 cache
6. 校验 cache
7. 写 generation 内文件
8. flush
9. 生成 cached_key.cache.tmp
10. rename cached_key.cache.tmp -> cached_key.cache
11. 删除旧 generation
12. 删除 ZIP
```

不要：

```text
先写 cached_key.cache
再下载 cache
```

否则 C++ 可能读到 metadata，但目录不存在。

---

# 24. generation 切换机制

假设旧 Cache：

```text
generations/
└── old-generation/
    ├── cached_offsets.cache
    └── cached_bitfields.cache
```

新 Cache：

```text
generations/
└── new-generation/
    ├── cached_offsets.cache
    └── cached_bitfields.cache
```

首先：

```text
cached_key.cache
```

仍然指向：

```text
old-generation
```

新 Cache 全部准备完成后：

```text
cached_key.cache
        ↓
new-generation
```

最后删除：

```text
old-generation
```

这样不会出现：

```text
cached_key.cache
    ↓
文件不存在
```

---

# 25. ZIP 安全要求

Go 解压时不要允许：

```text
../../xxx
```

也不要允许：

```text
..\..\xxx
```

也不要允许绝对路径：

```text
C:\xxx
```

当前 C++ 的 ZIP 解压逻辑实际上只接受固定文件名：

```text
cached_offsets.cache
cached_bitfields.cache
cached_key.cache
cached_offsets.txt
```

Go 最好完全保持一致。

---

# 26. ZIP 文件数量

C++：

```cpp
globalInfo.number_entry < 2
|| globalInfo.number_entry > 4
```

所以：

```text
最少：2
最多：4
```

推荐 ZIP：

```text
2 个文件
```

即：

```text
cached_offsets.cache
cached_bitfields.cache
```

---

# 27. Cache 大小限制

C++：

```text
整个 ZIP：
最大 768 MiB

单个 cache entry：
最大 512 MiB

解压后总大小：
最大 768 MiB
```

Go 应该保持一致。

---

# 28. Go 项目建议结构

建议：

```text
ark-cache-downloader/
├── go.mod
├── main.go
├── downloader.go
├── cache.go
├── metadata.go
├── archive.go
├── sha256.go
└── README.md
```

职责：

```text
main.go
    CLI

downloader.go
    HTTP
    Range
    retry
    .part

sha256.go
    EXE SHA256

archive.go
    ZIP
    安全解压
    文件检查

metadata.go
    cached_key.cache

cache.go
    Cache 目录
    generation
    cleanup
```

---

# 29. 推荐 CLI

最简单：

```bash
ark-cache-downloader.exe
```

自动寻找当前目录：

```text
./ArkAscendedServer.exe
```

推荐支持：

```bash
ark-cache-downloader.exe \
    --server-dir "D:\ArkServer"
```

以及：

```bash
ark-cache-downloader.exe \
    --server-dir "D:\ArkServer" \
    --cache-url "https://cdn.pelayori.com/cache/"
```

以及：

```bash
ark-cache-downloader.exe \
    --server-dir "D:\ArkServer" \
    --force
```

---

# 30. 推荐配置

例如：

```yaml
server_dir: "D:\\ArkServer"

cache:
  urls:
    - "https://cdn.pelayori.com/cache/"
    - "https://cdn.shadowhunter.co.za/cache/"
    - "https://cdn.shadowhunter-systems.co.za/cache/"

  max_size: 805306368

  retry:
    max_attempts: 0
    initial_delay: 1s
    max_delay: 30s
```

`max_attempts: 0` 可以定义为无限重试。

---

# 31. 最终目录示例

假设：

```text
SHA256 =
0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

最终：

```text
ArkServer/
├── ArkAscendedServer.exe
│
└── ArkApi/
    └── Cache/
        ├── cached_key.cache
        │
        └── generations/
            └── 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef-1234-1756912345678-0/
                ├── cached_offsets.cache
                └── cached_bitfields.cache
```

---

# 32. cached_key.cache 示例

```json
{
  "version": 1,
  "executable_hash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "last_modified": "Thu, 03 Sep 2026 15:20:10 GMT",
  "cache_directory": "generations/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef-1234-1756912345678-0"
}
```

---

# 33. 完整 Go 实现骨架

## sha256.go

```go
package cache

import (
    "crypto/sha256"
    "encoding/hex"
    "io"
    "os"
)

func SHA256File(path string) (string, error) {
    f, err := os.Open(path)
    if err != nil {
        return "", err
    }
    defer f.Close()

    h := sha256.New()

    if _, err := io.Copy(h, f); err != nil {
        return "", err
    }

    return hex.EncodeToString(h.Sum(nil)), nil
}
```

---

## metadata.go

```go
package cache

import (
    "encoding/json"
    "os"
)

type CacheMetadata struct {
    Version        int    `json:"version"`
    ExecutableHash string `json:"executable_hash"`
    LastModified   string `json:"last_modified"`
    CacheDirectory string `json:"cache_directory"`
}

func WriteMetadataAtomic(
    path string,
    metadata CacheMetadata,
) error {

    data, err := json.Marshal(metadata)
    if err != nil {
        return err
    }

    tmp := path + ".tmp"

    if err := os.WriteFile(tmp, data, 0644); err != nil {
        return err
    }

    return os.Rename(tmp, path)
}
```

---

# 34. Range 下载核心实现

```go
func DownloadResume(
    client *http.Client,
    url string,
    partPath string,
    maxSize int64,
) error {

    var offset int64

    if info, err := os.Stat(partPath); err == nil {
        offset = info.Size()
    }

    req, err := http.NewRequest(
        http.MethodGet,
        url,
        nil,
    )
    if err != nil {
        return err
    }

    if offset > 0 {
        req.Header.Set(
            "Range",
            fmt.Sprintf("bytes=%d-", offset),
        )
    }

    resp, err := client.Do(req)
    if err != nil {
        return err
    }

    defer resp.Body.Close()

    if offset > 0 &&
        resp.StatusCode == http.StatusPartialContent {

        f, err := os.OpenFile(
            partPath,
            os.O_WRONLY|os.O_APPEND,
            0644,
        )
        if err != nil {
            return err
        }

        defer f.Close()

        _, err = io.Copy(
            f,
            io.LimitReader(
                resp.Body,
                maxSize-offset+1,
            ),
        )

        if err != nil {
            return err
        }

        return f.Sync()
    }

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf(
            "unexpected HTTP status: %s",
            resp.Status,
        )
    }

    f, err := os.OpenFile(
        partPath,
        os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
        0644,
    )
    if err != nil {
        return err
    }

    defer f.Close()

    _, err = io.Copy(
        f,
        io.LimitReader(
            resp.Body,
            maxSize+1,
        ),
    )

    if err != nil {
        return err
    }

    return f.Sync()
}
```

生产代码还应该额外验证：

```text
Content-Range
Content-Length
最终文件大小
ETag
Last-Modified
```

---

# 35. 下载完成判断

不能简单：

```text
HTTP 200
```

就认为下载完成。

推荐：

```text
Content-Length = N

.part size == N
        |
        v
下载完成
```

Range：

```text
Content-Range:
bytes N-M/TOTAL
```

最终：

```text
.part size == TOTAL
```

才提交。

---

# 36. ZIP 解压完成后的最低检查

```text
generation/
├── cached_offsets.cache       必须存在
└── cached_bitfields.cache     必须存在
```

并且：

```text
size > 0
```

更严格：

最好进一步复刻：

```cpp
Cache::validateSerializedMap<intptr_t>()
Cache::validateSerializedMap<BitField>()
```

如果没有 `Cache.cpp`，Go 暂时只能做：

```text
文件存在
非空
大小合理
```

真正的二进制格式校验仍由 C++ 完成。

---

# 37. 建议下一步获取 Cache.cpp

如果后续需要让 Go 完全独立判断 Cache 是否有效，需要研究：

```text
Private/Cache.cpp
Private/Cache.h
```

重点是：

```cpp
validateSerializedMap<intptr_t>()
validateSerializedMap<BitField>()
deserializeMap<intptr_t>()
deserializeMap<BitField>()
```

有了这部分，就可以在 Go 中做到：

```text
下载
 ↓
ZIP 校验
 ↓
Cache 二进制格式校验
 ↓
只有真正有效才生成 cached_key.cache
```

---

# 38. 不建议的方案

## 不建议 1：直接下载到最终 ZIP

```text
Cache/hash.zip
```

失败后容易被误认为完整文件。

应该：

```text
Cache/hash.zip.part
```

---

## 不建议 2：失败后删除 `.part`

错误：

```text
timeout
 ↓
remove(hash.zip.part)
 ↓
重新下载
```

这正是当前问题。

应该：

```text
timeout
 ↓
保留 .part
 ↓
Range resume
```

---

## 不建议 3：直接覆盖当前 generation

不要：

```text
generations/current/
```

然后：

```text
删除旧文件
重新写
```

应该创建新的：

```text
generations/<new-generation>/
```

完成后再切换 metadata。

---

## 不建议 4：先修改 cached_key.cache

必须：

```text
Cache 完整准备
        ↓
metadata 最后提交
```

---

# 39. 推荐最终架构

```text
                 ┌─────────────────────┐
                 │ Go Cache Downloader │
                 └──────────┬──────────┘
                            │
                    SHA256 EXE
                            │
                            ▼
                 <sha256>.zip
                            │
                     HTTP Range
                            │
                 ┌──────────┴──────────┐
                 │                     │
              成功                    失败
                 │                     │
                 │                 保留 .part
                 │                     │
                 │                 retry + Range
                 │                     │
                 └──────────┬──────────┘
                            │
                            ▼
                       完整 ZIP
                            │
                            ▼
              generations/<generation>
                            │
                 ┌──────────┴──────────┐
                 │                     │
          cached_offsets        cached_bitfields
                 │                     │
                 └──────────┬──────────┘
                            │
                         validate
                            │
                            ▼
                  cached_key.cache
                            │
                            ▼
                    Cache Ready
                            │
                            ▼
                ArkAscendedServer.exe
                            │
                            ▼
                    ArkApi::Init()
                            │
                            ▼
                     只读取本地 Cache
```

---

# 40. 最终实施 checklist

### Go 下载器

- [ ] 计算 `ArkAscendedServer.exe` SHA256
- [ ] 拼接 `<hash>.zip`
- [ ] 支持多个 CDN
- [ ] 支持 HTTP Range
- [ ] 使用 `.part`
- [ ] 网络失败保留 `.part`
- [ ] Range 续传
- [ ] 检查 `206`
- [ ] `200` 时避免 append
- [ ] 检查 Content-Length
- [ ] 检查 Content-Range
- [ ] 限制最大下载大小 768 MiB
- [ ] 下载完成后再提交 ZIP

### ZIP

- [ ] ZIP entry 数量 2~4
- [ ] 只接受指定文件名
- [ ] 防止路径穿越
- [ ] 单文件 <= 512 MiB
- [ ] 总解压大小 <= 768 MiB
- [ ] `cached_offsets.cache` 必须存在
- [ ] `cached_bitfields.cache` 必须存在

### generation

- [ ] `Cache/generations/`
- [ ] `<sha256>-<number>-<number>-<number>`
- [ ] 新 generation 独立创建
- [ ] 不覆盖旧 generation

### metadata

- [ ] `version = 1`
- [ ] `executable_hash = SHA256(EXE)`
- [ ] `last_modified`
- [ ] `cache_directory = generations/<generation>`
- [ ] 原子写入 `cached_key.cache`

### ArkServerAPI

- [ ] `AutomaticCacheDownload.Enable = false`
- [ ] Go Downloader 在 ArkServer 启动之前运行
- [ ] Cache Ready 后再启动 ArkServer

---

# 41. 最终推荐运行方式

```text
启动脚本
   |
   v
ark-cache-downloader.exe
   |
   +-- Cache 已有效
   |       |
   |       +-- 直接返回
   |
   +-- Cache 不存在/EXE 更新
           |
           +-- Range 下载
           +-- 解压
           +-- 校验
           +-- 生成 metadata
           +-- 清理旧 Cache
   |
   v
ArkAscendedServer.exe
```

例如 Windows：

```bat
@echo off

ark-cache-downloader.exe --server-dir "%~dp0"

if errorlevel 1 (
    echo Cache preparation failed.
    exit /b 1
)

start "" "%~dp0ArkAscendedServer.exe"
```

---

## 42. 与当前 C++ 源码的关键对应关系

| C++ 行为 | Go 必须对应 |
|---|---|
| `SHA256(ArkAscendedServer.exe)` | Go SHA256 |
| `<hash>.zip` | Go 下载 URL |
| `Cache/` | Go 输出根目录 |
| `generations/` | Go generation 根目录 |
| `<hash>-pid-tick-suffix` | Go generation 名称 |
| `cached_offsets.cache` | ZIP 必须解压 |
| `cached_bitfields.cache` | ZIP 必须解压 |
| `cached_key.cache` | Go 自行生成 |
| `version = 1` | metadata version |
| `executable_hash` | 当前 EXE SHA256 |
| `cache_directory` | `generations/<generation>` |
| `AutomaticCacheDownload=false` | 禁止 C++ 下载 |
| `InspectLocalCache()` | C++ 启动时验证本地 Cache |

---

## 43. 一个重要的实现原则

**不要修改 Cache 文件本身。**

Go 的职责应该只是：

```text
下载官方 ZIP
        ↓
原样解压
        ↓
放入正确 generation
        ↓
生成 metadata
```

不要尝试重新序列化：

```text
cached_offsets.cache
cached_bitfields.cache
```

除非已经研究清楚 `Cache.cpp` 的二进制格式。

这样可以最大程度保证 Go 生成的 Cache 与原 ArkServerAPI 下载得到的 Cache 完全一致。

---

## 44. 结论

最终只需要保证：

```text
ArkApi/
└── Cache/
    ├── cached_key.cache
    └── generations/
        └── <SHA256>-<number>-<number>-<number>/
            ├── cached_offsets.cache
            └── cached_bitfields.cache
```

其中：

```text
cached_key.cache
```

必须指向正确的 generation，并且：

```text
executable_hash
```

必须等于：

```text
SHA256(ArkAscendedServer.exe)
```

然后将：

```json
{
  "settings": {
    "AutomaticCacheDownload": {
      "Enable": false
    }
  }
}
```

配置好。

这样 ArkServerAPI 启动时就不会再尝试下载 Cache，而是直接使用 Go 工具准备好的本地 Cache。

> 本文最重要的后续工作是实现 `Cache.cpp` 中 `validateSerializedMap()` 的 Go 版本。如果需要做到“Go 下载器自己确认 Cache 一定能被 ArkApi 读取”，应继续获取 `Cache.cpp` / `Cache.h` 并复刻其二进制格式验证逻辑。
