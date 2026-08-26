// Package logger 是全项目唯一的日志入口：包级函数直接调用即可写日志
// （logger.Infof("...", x)），不需要先取一个实例。默认只写文件；需要同时在控制台
// 可见时链式调用 logger.WithConsole().Infof(...)。
//
// 文件是否落盘只看一件事：这条日志的级别有没有达到 SetLevel 配置的阈值，与有没有链
// WithConsole() 无关。WithConsole() 只决定"要不要额外上屏"，一旦调用就无条件上屏
// （不受文件级别阈值影响）——这是两件独立发生、可以同时成立的事，不是互斥的两条路径。
//
// 不认识任何领域概念、零 internal/ 依赖，因此放在 pkg/ 下而不是 internal/：
// 详见 docs/LOGGER_REDESIGN_PLAN.md。
package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Logger 是本包对外暴露的日志接口，方法形状对齐 *zap.SugaredLogger
// （Xxx 是 fmt.Sprint 风格，Xxxf 是 fmt.Sprintf 风格），不是结构化的 zap.Field 风格——
// 全项目现有几百处 .Infof("...%v", x) 调用因此不需要重写。
type Logger interface {
	Debug(args ...any)
	Debugf(template string, args ...any)
	Info(args ...any)
	Infof(template string, args ...any)
	Warn(args ...any)
	Warnf(template string, args ...any)
	Error(args ...any)
	Errorf(template string, args ...any)
	Panic(args ...any)
	Panicf(template string, args ...any)
	Fatal(args ...any)
	Fatalf(template string, args ...any)

	// With 派生一个带固定字段的子 logger，签名对齐 SugaredLogger.With
	// （接受交替 key-value，也接受直接传 zap.Field）。
	With(args ...any) Logger

	// Named 派生一个带命名空间前缀的子 logger（体现在 JSON 的 logger 字段里）。
	Named(name string) Logger

	// WithConsole 派生一个"同时上屏"的子 logger：返回值调用 Info/Warn/... 时，
	// 除了照常按 SetLevel 配置的阈值写文件之外，无条件在控制台再打一份，不受该阈值影响——
	// 显式调用 WithConsole 就是在说"这条我要让人当场看见"。不调用 WithConsole 的
	// Logger（包括包级函数 Debug/Info/...）只写文件，不上屏。
	WithConsole() Logger

	// Sync 刷盘，服务退出前调用。
	Sync() error
}

const defaultLogFileName = "asaServer.log"

var (
	// BaseDir 由 InitLoggerWithBaseDir 设置。
	BaseDir = ""

	// levelAtomic 是文件 sink 的最低级别，控制台 sink（仅 WithConsole() 链路可达）
	// 不受它约束，见包注释与 Logger.WithConsole 的说明。
	levelAtomic = zap.NewAtomicLevelAt(zapcore.InfoLevel)

	logFilePath string

	defaultLogger Logger             // With/Named/WithConsole 包级函数从它派生
	pkgLogger     *zap.SugaredLogger // 包级 Debug/Info/Warn/... 直接调用它

	currentFileWriter *lumberjack.Logger // 供 Close() 释放文件句柄，见该函数注释
)

func init() {
	// InitLoggerWithBaseDir 调用之前的兜底实例：纯控制台、无文件、不碰磁盘
	// （不做 MkdirAll，导入本包本身不应该有任何副作用），避免过早调用时空指针 panic。
	setLoggers(newFallbackLogger())
}

// options 是 InitLoggerWithBaseDir 的构造参数，用 Option 函数式选项设置。
type options struct {
	fileName string
}

// Option 是 InitLoggerWithBaseDir 的函数式选项。与 Logger 接口的链式方法
// （With/Named/WithConsole）是两回事：Option 只在构造时起作用。
type Option func(*options)

// WithLogFileName 覆盖日志文件名，默认 "asaServer.log"。
func WithLogFileName(name string) Option {
	return func(o *options) { o.fileName = name }
}

// InitLoggerWithBaseDir 用给定 BaseDir 初始化日志系统，落盘到
// {BaseDir}/logs/{fileName}。可以多次调用重新初始化（比如 BaseDir 运行期变化）。
func InitLoggerWithBaseDir(baseDir string, opts ...Option) {
	BaseDir = baseDir
	o := options{fileName: defaultLogFileName}
	for _, opt := range opts {
		opt(&o)
	}
	buildLoggers(baseDir, o)
}

// GetLogFilePath 返回系统日志文件的完整路径，供 internal/webapi/logapi 之类的调用方
// 定位文件做 tail。这个函数本身是有意义的查询，不属于要移除的 GetLogger()/GetStdout()
// 那类多余取实例动作。
func GetLogFilePath() string {
	if logFilePath != "" {
		return logFilePath
	}
	dir := BaseDir
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "logs", defaultLogFileName)
}

// SetLevel 运行时调整文件 sink 的最低级别（同时也是"上屏但不达标"这个说法里的达标线，
// 详见包注释），非法或空字符串忽略、保留当前值。
func SetLevel(level string) {
	if level == "" {
		return
	}
	var lv zapcore.Level
	if err := lv.UnmarshalText([]byte(level)); err != nil {
		return
	}
	levelAtomic.SetLevel(lv)
}

// Close closes the current underlying log file handle. Production code
// doesn't need to call this (the process exiting releases it anyway); it
// exists for tests that re-initialize the logger repeatedly within a single
// process and need the previous file handle released before removing its
// temp directory (Windows refuses to delete a file that's still open).
func Close() error {
	if currentFileWriter == nil {
		return nil
	}
	return currentFileWriter.Close()
}

func buildLoggers(baseDir string, o options) {
	logFilePath = filepath.Join(baseDir, "logs", o.fileName)
	if err := os.MkdirAll(filepath.Dir(logFilePath), 0755); err != nil {
		panic(err)
	}

	fileCore := zapcore.NewCore(fileEncoder(), fileWriter(logFilePath), levelAtomic)
	// consoleCore 没有级别下限：WithConsole() 链路上的调用无条件可见，不受 levelAtomic 约束。
	consoleCore := zapcore.NewCore(stdoutEncoder(), stdoutWriter(), zapcore.DebugLevel)

	activeZap := zap.New(fileCore, zap.AddCaller(), zap.AddCallerSkip(1))
	consoleZap := zap.New(zapcore.NewTee(fileCore, consoleCore), zap.AddCaller(), zap.AddCallerSkip(1))

	setLoggers(&sugaredLogger{active: activeZap.Sugar(), console: consoleZap.Sugar()})
}

// newFallbackLogger 是 init() 用的兜底实例，见该处注释。
func newFallbackLogger() *sugaredLogger {
	core := zapcore.NewCore(stdoutEncoder(), stdoutWriter(), zapcore.InfoLevel)
	z := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
	return &sugaredLogger{active: z, console: z}
}

// setLoggers 同时更新 With/Named/WithConsole 用的 defaultLogger，与包级
// Debug/Info/Warn/... 用的 pkgLogger——后者比前者多跳过一级调用栈，对应包级函数本身
// 这层包装占用的调用帧，否则日志里的 caller 文件:行号会指向本文件而不是真正的调用处。
func setLoggers(l *sugaredLogger) {
	defaultLogger = l
	pkgLogger = l.active.WithOptions(zap.AddCallerSkip(1))
}

// ---- 包级函数：唯一的调用入口，不再有 GetLogger()/GetStdout() ----

func Debug(args ...any)                   { pkgLogger.Debug(args...) }
func Debugf(template string, args ...any) { pkgLogger.Debugf(template, args...) }
func Info(args ...any)                    { pkgLogger.Info(args...) }
func Infof(template string, args ...any)  { pkgLogger.Infof(template, args...) }
func Warn(args ...any)                    { pkgLogger.Warn(args...) }
func Warnf(template string, args ...any)  { pkgLogger.Warnf(template, args...) }
func Error(args ...any)                   { pkgLogger.Error(args...) }
func Errorf(template string, args ...any) { pkgLogger.Errorf(template, args...) }
func Panic(args ...any)                   { pkgLogger.Panic(args...) }
func Panicf(template string, args ...any) { pkgLogger.Panicf(template, args...) }
func Fatal(args ...any)                   { pkgLogger.Fatal(args...) }
func Fatalf(template string, args ...any) { pkgLogger.Fatalf(template, args...) }

func With(args ...any) Logger  { return defaultLogger.With(args...) }
func Named(name string) Logger { return defaultLogger.Named(name) }
func WithConsole() Logger      { return defaultLogger.WithConsole() }

// Sync 刷盘，服务退出前调用。
func Sync() error { return defaultLogger.Sync() }

// ---- sugaredLogger：Logger 接口的实现，内部持有两棵 zap 树 ----

type sugaredLogger struct {
	active  *zap.SugaredLogger // 当前生效的一个：默认是"只文件"
	console *zap.SugaredLogger // "文件 + 控制台"的等价版本，WithConsole() 切过去
}

func (z *sugaredLogger) Debug(args ...any)                   { z.active.Debug(args...) }
func (z *sugaredLogger) Debugf(template string, args ...any) { z.active.Debugf(template, args...) }
func (z *sugaredLogger) Info(args ...any)                    { z.active.Info(args...) }
func (z *sugaredLogger) Infof(template string, args ...any)  { z.active.Infof(template, args...) }
func (z *sugaredLogger) Warn(args ...any)                    { z.active.Warn(args...) }
func (z *sugaredLogger) Warnf(template string, args ...any)  { z.active.Warnf(template, args...) }
func (z *sugaredLogger) Error(args ...any)                   { z.active.Error(args...) }
func (z *sugaredLogger) Errorf(template string, args ...any) { z.active.Errorf(template, args...) }
func (z *sugaredLogger) Panic(args ...any)                   { z.active.Panic(args...) }
func (z *sugaredLogger) Panicf(template string, args ...any) { z.active.Panicf(template, args...) }
func (z *sugaredLogger) Fatal(args ...any)                   { z.active.Fatal(args...) }
func (z *sugaredLogger) Fatalf(template string, args ...any) { z.active.Fatalf(template, args...) }

func (z *sugaredLogger) With(args ...any) Logger {
	return &sugaredLogger{active: z.active.With(args...), console: z.console.With(args...)}
}

func (z *sugaredLogger) Named(name string) Logger {
	return &sugaredLogger{active: z.active.Named(name), console: z.console.Named(name)}
}

func (z *sugaredLogger) WithConsole() Logger {
	return &sugaredLogger{active: z.console, console: z.console}
}

func (z *sugaredLogger) Sync() error {
	err := z.active.Sync()
	// z.console 包含 os.Stdout，在部分 Windows 控制台环境下 Sync() 已知会返回
	// "The handle is invalid" 之类的误报；它内部的文件 core 部分已经被上面
	// z.active.Sync() 覆盖过一次，这里的错误统一忽略，不让误报冒泡成真错误。
	_ = z.console.Sync()
	return err
}

// ---- encoder/writer ----

func stdoutEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	return zapcore.NewConsoleEncoder(encoderConfig)
}

// fileEncoder 的键名必须保持 zap 默认值（ts/level/msg/...）：
// app/src/views/SystemLogs.vue 直接按这几个默认键名解析系统日志 SSE 的每一行，
// 改键名会静默破坏前端，见 docs/LOGGER_REDESIGN_PLAN.md §3。
func fileEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	return zapcore.NewJSONEncoder(encoderConfig)
}

func fileWriter(path string) zapcore.WriteSyncer {
	absPath, _ := filepath.Abs(path)
	lj := &lumberjack.Logger{
		Filename:   absPath,
		MaxSize:    15, // MB
		MaxBackups: 10,
		MaxAge:     7, // days
		Compress:   true,
	}
	currentFileWriter = lj
	return zapcore.AddSync(lj)
}

func stdoutWriter() zapcore.WriteSyncer {
	return zapcore.AddSync(os.Stdout)
}
