package instance

// ArkApi（AsaApiLoader.exe）的业务日志**不走控制台**，只写文件：
//
//	<游戏 exe 目录>/logs/ArkApi_<wine 侧 PID>_<YYYY-MM-DD_HH-MM>.log
//
// 实例场景下「游戏 exe 目录」是镜像里的 ShooterGame/Binaries/Win64/，每次启动一个
// 新文件，轮转由 ArkApi 自己按 config.json 的 DeleteOldLogs 处理。
//
// 这件事在 Linux 上很要命：那里 PTY 里跑的是 umu-run 整条包装链，不是加载器本体，
// 所以实例的 arkAsaApi.log 收到的全是 umu/pressure-vessel/Proton 的噪声，
// ArkApi 的内容一行都没有。而且「把噪声过滤掉」是行不通的 —— 过滤完是空的。
// 见 docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md §1。
//
// 本文件是「找到这次启动对应的那个日志文件」的纯逻辑部分，不加构建约束：全是路径与
// 时间的比较，没有平台专属 API，不加约束才能在 Windows 上跑单测。转抄协程在
// asaapilog_linux.go。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// arkApiLogDirRel 是 ArkApi 日志目录相对游戏根目录的位置。
const arkApiLogDirRel = "ShooterGame/Binaries/Win64/logs"

// ArkApi 日志的文件名形状。用宽松的前后缀匹配而不是精确解析 PID 与时间戳：
// 上游改了格式时我们只会挑错文件，而不是一个都挑不到。
const (
	arkApiLogPrefix = "ArkApi_"
	arkApiLogSuffix = ".log"
)

// ErrNoArkApiLog 表示这次启动还没有（或没能）产生 ArkApi 日志。
// 是个正常状态而非故障：加载器要先下载 offsets cache 才会开始写日志。
var ErrNoArkApiLog = errors.New("尚未找到本次启动的 ArkApi 日志")

// arkApiLogDir 返回某个实例镜像里的 ArkApi 日志目录。
func arkApiLogDir(mirrorDir string) string {
	return filepath.Join(mirrorDir, filepath.FromSlash(arkApiLogDirRel))
}

// newestArkApiLog 返回 dir 下**不早于 notBefore** 的最新一个 ArkApi 日志。
//
// notBefore 这个闸门不是可选的：镜像是**增量**同步的（mirror.SyncInstanceMirror），
// 上几次启动留下的 ArkApi_*.log 还在原地。没有它，转抄协程会在本次日志出现之前
// 一直误认上一次的那份，把陈旧内容当成实时输出贴给用户。
func newestArkApiLog(dir string, notBefore time.Time) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ErrNoArkApiLog
	}

	var (
		newestPath string
		newestAt   time.Time
	)
	for _, e := range entries {
		if e.IsDir() || !isArkApiLogName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Before 而非 !After：mtime 与 notBefore 相等时算本次的。
		if info.ModTime().Before(notBefore) {
			continue
		}
		if newestPath == "" || info.ModTime().After(newestAt) {
			// filepath.Base 收敛：文件名是 ArkApi 写的，不是我们生成的。
			newestPath, newestAt = filepath.Join(dir, filepath.Base(e.Name())), info.ModTime()
		}
	}
	if newestPath == "" {
		return "", ErrNoArkApiLog
	}
	return newestPath, nil
}

func isArkApiLogName(name string) bool {
	return strings.HasPrefix(name, arkApiLogPrefix) && strings.HasSuffix(name, arkApiLogSuffix)
}
