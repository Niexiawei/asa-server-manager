# ARK 服务器实例 v2 镜像启动方式技术文档

## 一、概述

v2 启动方式的核心改进是将 v1 的**共享目录 + 全局 NTFS Junction** 替换为**独立镜像目录**方案。每个服务器实例在 `server-files-tmp-<instanceName>/` 下拥有独立的镜像目录，通过 NTFS Junction 和 symlink 链接到原始文件，消除了 v1 中同一时间只能启动一个实例的全局锁限制。

## 二、核心思想

### 2.1 目录结构

```
server-files/                              ← 原始服务器文件（只读）
├── ShooterGame/
│   ├── Binaries/Win64/                    ← 整个目录需完整复制到镜像（隔离启动期缓存）
│   │   ├── ArkAscendedServer.exe
│   │   └── AsaApiLoader.exe
│   ├── Content/                           ← 资源文件（可 symlink / junction）
│   └── Saved/
│       ├── Config/WindowsServer/          ← exception target → junction 到实例 Config
│       ├── Logs/                          ← exception target → junction 到实例 Logs
│       └── <SaveDir>/                     ← exception target → junction 到实例 Save

server-files-tmp-<instanceName>/           ← 每实例独立镜像
├── ShooterGame/
│   ├── Binaries/Win64/                    ← 真实目录，内部所有文件均为真实副本
│   │   ├── ArkAscendedServer.exe
│   │   └── AsaApiLoader.exe
│   ├── Content/ → (junction → server-files/ShooterGame/Content/)
│   └── Saved/
│       ├── Config/WindowsServer/ → (junction → instances/<name>/Config/)
│       ├── Logs/ → (junction → instances/<name>/Logs/)
│       └── <SaveDir>/ → (junction → instances/<name>/Save/)

instances/<name>/                          ← 每实例本地存储
├── instance_config.ini
├── Config/
│   ├── Game.ini
│   └── GameUserSettings.ini
├── Logs/
│   └── ShooterGame.log
└── Save/
    └── <MapName>/
        └── <MapName>.ark
```

### 2.2 文件处理策略

> **变更说明**：早期版本对「exe 文件」和「包含 exe 的目录」有单独的复制特判（`exeFiles` / `containsExeFiles`）。
> 由于两个 exe（`ArkAscendedServer.exe` / `AsaApiLoader.exe`）都位于 `Binaries/Win64` 内，而该目录现已**整体完整复制**，
> exe 特判被 `isUnderWin64` 判断完全覆盖，属冗余，已移除。现在的策略只按「是否位于 `Binaries/Win64` 内」区分。

**目录**：

| 目录类型 | 处理方式 | 原因 |
|----------|----------|------|
| **Exception target 目录**（Config/Logs/SaveDir） | NTFS Junction → 实例本地目录 | 游戏引擎需要 per-instance 读写 |
| **包含 exception 子目录的父目录** | 真实目录（不 junction） | 需要容纳下级 junction |
| **`Binaries/Win64` 目录及其所有子目录** | 真实目录（不 junction） | 需要容纳整体复制的文件与启动期缓存 |
| **Win64 的祖先目录**（`ShooterGame`、`ShooterGame/Binaries`） | 真实目录（不 junction） | 必须真实才能容纳隔离的真实 Win64 子目录；否则整体 junction 到源会让各实例共享同一份 Win64 |
| **其他普通目录** | NTFS Junction → 原始目录 | 节省磁盘空间，免复制 |

**文件**：

| 文件类型 | 处理方式 | 失败回退 | 原因 |
|----------|----------|----------|------|
| **`.log` 日志文件** | 跳过，不镜像 | — | 运行期日志会造成增量 diff 抖动 |
| **`Binaries/Win64` 内的所有文件**（exe / .dll / .pak / .ucas 等） | 真实文件复制（`fsutil.CopyFile`），**不 symlink** | 复制失败则报错 | 启动过程中会在 Win64 内生成缓存文件，symlink 会使缓存落回原目录导致镜像读不到缓存而无法启动，故整体复制隔离 |
| **其他普通文件** | 文件 symlink → 原始文件 | **自动回退到 `fsutil.CopyFile` 完整复制** | 无 symlink 权限时仍需保证文件可用 |

### 2.3 Exception Targets（例外目标）

启动时通过 `buildExceptionTargets` 构建例外映射表：

```go
func buildExceptionTargets(instanceName string, cfg *InstanceConfig) map[string]string {
    return map[string]string{
        "ShooterGame/Saved/Config/WindowsServer":  filepath.Join(InstancesDir, instanceName, "Config"),
        "ShooterGame/Saved/Logs":                  filepath.Join(InstancesDir, instanceName, "Logs"),
        "ShooterGame/Saved/" + cfg.SaveDir:        filepath.Join(InstancesDir, instanceName, "Save"),
    }
}
```

这三个路径是游戏引擎会读写的位置，需要指向各实例独立的存储空间。

## 三、Junction 创建机制

### 3.1 使用 Go 原生 os.Symlink

```go
// mirror.go
func createJunction(linkPath, targetPath string) error {
    absTarget, _ := filepath.Abs(targetPath)
    return os.Symlink(absTarget, linkPath)
}
```

**Go 1.21+ 在 Windows 上对目录目标的 `os.Symlink` 调用自动创建 NTFS Junction**（reparse point），无需管理员权限。

与 v1 使用 `cmd /c mklink /J` 的对比：

| 维度 | v1 (`cmd /c mklink /J`) | v2 (`os.Symlink`) |
|------|--------------------------|---------------------|
| 进程开销 | 启动 cmd.exe 子进程 | 无外部进程 |
| 权限 | 无需管理员，但有进程创建开销 | 无需管理员 |
| 安全性 | 命令注入风险 | 无注入风险 |
| 可靠性 | 依赖 cmd.exe 行为 | Go 标准库保证 |
| 错误处理 | 需解析 stdout/stderr | 直接返回 error |

### 3.2 Junction 与 Symlink 的区别

在 Windows 上，`os.Symlink` 根据目标类型表现不同：

- **目录目标** → 创建 NTFS Junction（reparse point），不需要管理员权限
- **文件目标** → 创建文件 symlink，**需要管理员权限或开启开发者模式**

### 3.3 文件 Symlink 的 Copy 回退机制

Windows 对文件 symlink 的权限要求比目录 junction 严格得多。`os.Symlink` 在 Windows 上的行为取决于目标类型和当前进程权限：

| 目标类型 | 权限要求 | `os.Symlink` 结果 |
|----------|----------|-------------------|
| **目录** | 无需管理员 | 成功创建 NTFS Junction（reparse point） |
| **文件** | 需要管理员 **或** 开启开发者模式 | 有权限时成功，无权限时失败 |

文件 symlink 在 Windows 上失败的常见场景：
1. 进程以普通用户身份运行，且系统未开启"开发者模式"
2. 企业域控策略禁止创建 symlink
3. 杀毒软件拦截 symlink 创建

**v2 的回退策略**：当 `os.Symlink` 创建文件 symlink 失败时，自动回退到 `copyFile` 将文件完整复制到镜像中。这确保了即使在无 symlink 权限的环境下，镜像目录也能正常工作。

```go
// mirror.go — createFileSymlink 带 copy 回退
func createFileSymlink(linkPath, targetPath string) error {
    absTarget, _ := filepath.Abs(targetPath)

    // 确保父目录存在
    parentDir := filepath.Dir(linkPath)
    os.MkdirAll(parentDir, 0755)

    // 尝试创建文件 symlink
    if err := os.Symlink(absTarget, linkPath); err != nil {
        // symlink 失败 → 回退到完整复制
        if IsElevated() {
            // 以管理员运行仍然失败 → 异常情况，记录警告
            logger.Warnf("Symlink failed even with admin, fallback copy: %s: %v", linkPath, err)
        } else {
            // 普通用户 → 预期行为，记录调试信息
            logger.Debugf("No admin, fallback copy: %s", linkPath)
        }
        return fsutil.CopyFile(targetPath, linkPath)  // 完整复制文件
    }

    // symlink 成功
    logger.Debugf("Created file symlink: %s -> %s", linkPath, absTarget)
    return nil
}
```

#### 回退决策流程图

```
createFileSymlink(linkPath, targetPath)
    │
    ├── os.Symlink(absTarget, linkPath)
    │     │
    │     ├── 成功 → 返回 nil（文件 symlink 已创建）
    │     │
    │     └── 失败 →
    │           ├── IsElevated() == true ?
    │           │     ├── 是 → 记录 WARN（管理员仍失败，异常情况）
    │           │     └── 否 → 记录 DEBUG（普通用户，预期行为）
    │           │
    │           └── fsutil.CopyFile(targetPath, linkPath)  ← 回退到完整复制
    │                 │
    │                 ├── 创建父目录
    │                 ├── os.Open(src) + os.Create(dst)
    │                 ├── io.Copy(dst, src)
    │                 └── os.Chmod(dst, srcInfo.Mode())  ← 保留文件权限
```

#### 哪些文件会被回退复制？

在镜像创建流程中，只有**位于 `Binaries/Win64` 之外的普通文件**会走 `createFileSymlink` 路径：

| 文件类型 | 处理方式 | 走 symlink+copy 回退？ |
|----------|----------|----------------------|
| `Binaries/Win64` 内的文件（含 exe / dll 等） | 直接 `fsutil.CopyFile`（整体复制） | ❌ 不走，直接复制 |
| Win64 外的普通文件（.pak, .ucas 等） | `createFileSymlink` → 失败时 copy | ✅ 是 |
| 父目录是 junction 的文件 | 跳过（通过 junction 已可访问） | ❌ 不走 |
| `.log` 日志文件 | 跳过，不镜像 | ❌ 不走 |

#### 回退复制的代价

当 symlink 回退到 copy 时，会产生以下影响：

| 维度 | Symlink（正常） | Copy 回退 |
|------|-----------------|-----------|
| 镜像占用空间 | 0（仅链接） | 与源文件相同大小 |
| 创建速度 | 即时（创建链接） | 取决于文件大小和磁盘速度 |
| 更新同步 | 无需更新（链接自动指向最新） | 增量同步时需 MD5 比对 + 重新复制 |
| 游戏运行时行为 | 透明读取原文件 | 读取本地副本，与源文件独立 |

**实际影响评估**：ASA 服务器的 `Content/` 目录包含大量 `.pak`、`.ucas`、`.utoc` 资源文件，总计可达 **20-40GB**。如果所有文件都回退到 copy，每个实例的磁盘占用将从近 0 增长到 20-40GB。

#### 开发者模式开启方法

为避免 copy 回退带来的磁盘空间问题，建议在运行 ASA Server Manager 的 Windows 系统上开启开发者模式：

```
Windows 设置 → 更新和安全 → 开发者选项 → 开发人员模式 → 开启
```

开启后，普通用户进程也可以创建文件 symlink，无需管理员权限。

#### IsElevated 检测

代码通过 `IsElevated()` 检测当前进程是否以管理员身份运行，仅用于决定日志级别：

```go
func IsElevated() bool {
    // 通过 Windows API 检查进程 token 是否包含管理员 SID
    var token windows.Token
    windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
    // 检查 BUILTIN\Administrators 组成员身份
    // 结果缓存在 sync.Once 中，只检测一次
}
```

注意：`IsElevated` **不决定是否回退**——无论是否管理员，symlink 失败都会回退到 copy。管理员检测仅影响日志级别（管理员失败记录 WARN，普通用户失败记录 DEBUG）。

## 四、镜像生命周期管理

### 4.1 全量创建 (`createInstanceMirror`)

首次启动实例时，遍历整个 `server-files/` 目录树，根据 §2.2 的处理策略创建 junction/symlink/real file：

```
1. filepath.Walk(server-files/)
   ├── 对每个目录调用 processDirectory()
   │     ├── 是 exception target → 创建 junction 到实例本地目录，跳过递归
   │     ├── 有 exception 子目录 → 创建真实目录，继续递归
   │     ├── 位于 Binaries/Win64 内（含子目录） → 创建真实目录，继续递归
   │     ├── 是 Win64 的祖先目录（ShooterGame/Binaries 等） → 创建真实目录，继续递归
   │     └── 其他 → 创建 junction 到原始目录，跳过递归
   │
   └── 对每个文件调用 processFile()
         ├── 是 .log 日志文件 → 跳过（不镜像）
         ├── 父目录是 junction → 跳过（通过 junction 已可访问）
         ├── 位于 Binaries/Win64 内 → 复制文件（fsutil.CopyFile，整体复制隔离缓存）
         └── 其他 → 创建文件 symlink（失败回退复制）

2. 补充缺失的 exception targets
   ├── 如果 instances/<name>/Config 不存在 → 创建目录 + junction
   ├── 如果 instances/<name>/Logs 不存在 → 创建目录 + junction
   └── 如果 instances/<name>/Save 不存在 → 创建目录 + junction
```

### 4.2 增量同步 (`syncMirrorEntries`)

当镜像已存在时，基于 diff 库计算源目录与镜像的差异，仅处理变更部分：

```
1. collectSourceEntries()  → 收集源目录所有条目（按 RelPath 排序）
2. collectMirrorEntries()  → 收集镜像所有条目（按 RelPath 排序）
3. diff.EditsFunc()        → 计算差异
     ├── Insert (源有、镜像无) → 创建新条目
     ├── Delete (镜像有、源无) → 安全删除
     └── Match (两者都有)     → reconcileEntry 校验
           ├── 真实文件（含 Binaries/Win64 内所有文件） → 逐文件 MD5 校验，不匹配则从原文件覆盖复制到镜像
           ├── Symlink → 检查目标是否正确 / 是否断裂，不正确则重建
           └── Junction → 通过 os.Lstat 检查，不正确则重建
4. 补充缺失的 exception targets
```

#### 删除同步（源目录删除文件 → 镜像同步删除）

当源 `server-files` 删除了某些文件/目录后，下次增量同步会命中 `diff.Delete`（源无、镜像有），调用 `removeMirrorEntry`（`mirror.go`）按条目类型**安全删除**：

| 镜像条目类型 | 删除方式 | 说明 |
|--------------|----------|------|
| junction / symlink | `os.Remove` | 只删链接本身，**不删链接目标** |
| 真实文件（`EntryTypeFile`，含 `Binaries/Win64` 复制文件） | `os.Remove` | 删除镜像内的真实副本 |
| 真实目录 | `os.RemoveAll` | 游戏运行时可能在其中写入，强制清除 |

各目录类型的删除表现：

- **`Binaries/Win64` 内**：源删除某文件 → `collectSourceEntries` 不再产出该条目 → 镜像内的真实副本在下次同步时被删除。
- **junction 目录（如 `Content/`）**：镜像只是一个 junction，不逐文件追踪，源删除通过 junction 天然透传，无需同步动作。
- **exception target（Config/Logs/Save）**：junction 指向实例本地目录，与源 `server-files` 无关，不受源删除影响。

**边界**：游戏运行期在镜像 Win64 内新生成、而源目录本就没有的缓存文件，属于「源无、镜像有」，会在下次同步时被 `diff.Delete` 清除——这是预期行为（缓存每次启动重新生成），不影响启动。删除同步仅发生在**镜像已存在的增量同步**路径；首次全量创建（`createInstanceMirror`）从空目录按源构建，不涉及「多余文件」删除。

### 4.3 清理 (`CleanupInstanceMirror`)

安全清理流程，仅移除链接，不删除链接目标：

```
1. Walk 镜像，收集所有条目 + 深度信息
2. 按深度降序排序（从最深层开始）
3. 第一轮：移除所有 junction 和 symlink（os.Remove 不跟随链接）
4. 第二轮：移除剩余真实文件，尝试删除空目录
5. 最后：移除镜像根目录（如果仍非空则用 RemoveAll）
```

## 五、与 v1 架构对比

### 5.1 全局锁消除

**v1 问题**：`setupInstanceConfig` 在共享的 `server-files/ShooterGame/Saved/Config/WindowsServer` 上创建 junction，同一时间只能有一个实例处于 `start_initialization`。状态机通过 `isAnyInstanceInitializingLocked()` 强制全局互斥。

**v2 解决**：每个实例拥有独立的镜像目录，不同实例的 junction 不相互影响。所有实例可以同时启动，`ForceStopServer` 也不需要等待其他实例释放 junction。

### 5.2 完整对比表

| 维度 | v1 (setupInstanceConfig) | v2 (mirror) |
|------|--------------------------|-------------|
| 全局锁 | 有 — 同一时间只能启动一个实例 | 无 — 所有实例可并行启动 |
| 修改共享目录 | 是 — 直接在 server-files 上创建 junction | 否 — 镜像在独立目录 |
| Backup/Restore | 需要 `WindowsServer.bak` | 不需要 |
| confReset 机制 | 需要 — 启动完成后释放 junction | 不需要 |
| 日志管理 | 全局映射文件 `.instance_log_mapping.json` | 每实例独立目录 `instances/<name>/Logs/` |
| exe 路径 | `server-files/.../ArkAscendedServer.exe` | `server-files-tmp-<name>/.../ArkAscendedServer.exe` |
| 工作目录 | `server-files/ShooterGame/Binaries/Win64` | `server-files-tmp-<name>/ShooterGame/Binaries/Win64` |
| Force stop | 需等 junction 释放 (WaitForNoInitializing) | 直接清理独立镜像 |
| 启动失败清理 | 移除 junction + 恢复备份 | CleanupInstanceMirror |
| 增量同步 | 无（每次全量处理） | 有（diff 库 + MD5 校验） |
| 存档路径 | `server-files/ShooterGame/Saved/<SaveDir>/` | `instances/<name>/Save/` |

### 5.3 启动并发度对比

```
v1 时间线：
  实例A启动 ████████████████████
  实例B启动                    ████████████████████   （需等 A 释放 junction）
  实例C启动                                          ████████████████████

v2 时间线：
  实例A启动 ████████████████████
  实例B启动 ████████████████████   （独立镜像，无需等待）
  实例C启动 ████████████████████   （独立镜像，无需等待）
```

## 六、磁盘空间分析

### 6.1 镜像空间占用

由于大部分目录和文件使用 NTFS Junction / symlink（仅创建链接，不复制数据），镜像实际占用的磁盘空间很小：

| 内容 | 处理方式 | 空间占用 |
|------|----------|----------|
| `Binaries/Win64` 目录（含 exe / dll 等全部文件） | 整体复制 | ~Win64 目录实际大小 × 实例数 |
| Exception target 目录 | Junction | 0（链接） |
| Content/ 等资源目录 | Junction | 0（链接） |
| 普通文件 | Symlink | 0（链接） |
| Config/Logs/Save | 真实文件（实例独立） | 取决于实际使用 |

**结论**：每个实例的额外磁盘开销主要是 `Binaries/Win64` 目录的整体副本（用于隔离启动期缓存），其他体量的 `Content/` 等资源目录仍为 junction/symlink，不占额外空间。

### 6.2 增量同步开销

首次创建镜像后，后续启动只需进行增量同步：
- 源/镜像文件列表对比：毫秒级
- MD5 校验：对 `Binaries/Win64` 目录内所有真实文件逐一校验，不一致则从原文件覆盖复制
- 仅在 server-files 更新（如游戏版本更新）后才有实际文件操作

## 七、容错机制

### 7.1 镜像创建失败

`createInstanceMirror` 在 `filepath.Walk` 失败或 exception junction 创建失败时，会自动调用 `CleanupInstanceMirror` 清理不完整的镜像。

### 7.2 增量同步失败

`syncMirrorEntries` 失败时，`SyncInstanceMirror` 会清理现有镜像并从头重建。

### 7.3 启动失败

`startServerInternal` 的 deferred 函数检查 `mirrorDir` 变量，如果镜像已创建但启动失败，自动调用 `CleanupInstanceMirror` 清理。

### 7.4 Force Stop

`ForceStopServer` 不需要等待 junction 释放（因为每个实例的 junction 是独立的），直接 kill 进程 + 清理镜像 + 写入 stopped 状态。
