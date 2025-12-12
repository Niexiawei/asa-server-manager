package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type LogMode int

const (
	CLIMode LogMode = iota
	ServicesMode
	HttpApiMode
)

var (
	logger       *zap.SugaredLogger
	loggerStdout *zap.SugaredLogger
	loggerPath   string
	loggerMode   = CLIMode
	BaseDir      = "" // Will be set by InitLoggerWithBaseDir
)

func SetLogMode(mode LogMode) {
	loggerMode = mode
}

func GetLogMode() LogMode {
	return loggerMode
}

func InitLoggerWithBaseDir(baseDir string) {
	BaseDir = baseDir
	InitLogger()
}

func GetLogger() *zap.SugaredLogger {
	return logger
}

func GetStdout() *zap.SugaredLogger {
	return loggerStdout
}

// GetLogFilePath returns the full path to the system log file
func GetLogFilePath() string {
	if BaseDir == "" {
		BaseDir = "."
	}
	return filepath.Join(BaseDir, "logs", "asaServer.log")
}

func InitLogger() {
	if BaseDir == "" {
		// Fallback to current directory if BaseDir not set
		BaseDir = "."
	}

	loggerPath = filepath.Join(BaseDir, "logs", "asaServer.log")
	dir := filepath.Dir(loggerPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(err)
	}

	{
		core := zapcore.NewCore(stdoutEncoder(), stdoutWriter(), zapcore.InfoLevel)
		_logger := zap.New(core, zap.AddCaller())
		loggerStdout = _logger.Sugar()
	}

	{
		core := zapcore.NewTee(
			zapcore.NewCore(stdoutEncoder(), stdoutWriter(), zapcore.WarnLevel),
			zapcore.NewCore(fileEncoder(), fileWriter(), zap.LevelEnablerFunc(func(level zapcore.Level) bool {
				return level >= zap.InfoLevel
			})),
		)
		_logger := zap.New(core, zap.AddCaller())
		logger = _logger.Sugar()
	}

}

func stdoutEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	return zapcore.NewConsoleEncoder(encoderConfig)
}

func fileEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	return zapcore.NewJSONEncoder(encoderConfig)
}

func fileWriter() zapcore.WriteSyncer {
	filePath, _ := filepath.Abs(loggerPath)
	ws := zapcore.AddSync(&lumberjack.Logger{
		Filename:   filePath, // file name
		MaxSize:    200,      // maximum file size (MB)
		MaxBackups: 10,       // maximum number of old files
		MaxAge:     7,        // maximum number of days for old documents
		Compress:   true,     // whether to compress and archive old files
	})
	return zapcore.AddSync(ws)
}

func stdoutWriter() zapcore.WriteSyncer {
	return zapcore.AddSync(os.Stdout)
}
