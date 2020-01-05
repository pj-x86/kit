package zap

import (
	"os"

	"github.com/go-kit/kit/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type zapSugarLogger func(msg string, keysAndValues ...interface{})

func (l zapSugarLogger) Log(kv ...interface{}) error {
	l("", kv...)
	return nil
}

// NewZapSugarLogger returns a Go kit log.Logger that sends
// log events to a zap.Logger.
func NewZapSugarLogger(logger *zap.Logger, level zapcore.Level) log.Logger {
	sugarLogger := logger.WithOptions(zap.AddCallerSkip(2)).Sugar()
	var sugar zapSugarLogger
	switch level {
	case zapcore.DebugLevel:
		sugar = sugarLogger.Debugw
	case zapcore.InfoLevel:
		sugar = sugarLogger.Infow
	case zapcore.WarnLevel:
		sugar = sugarLogger.Warnw
	case zapcore.ErrorLevel:
		sugar = sugarLogger.Errorw
	case zapcore.DPanicLevel:
		sugar = sugarLogger.DPanicw
	case zapcore.PanicLevel:
		sugar = sugarLogger.Panicw
	case zapcore.FatalLevel:
		sugar = sugarLogger.Fatalw
	default:
		sugar = sugarLogger.Infow
	}
	return sugar
}

func getEncoder(env string) zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder   //采用 ISO8601 时间格式
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder //日志级别名称全大写
	if env == "production" || env == "prod" {               //生产环境使用对日志监控系统友好的 json 格式输出
		return zapcore.NewJSONEncoder(encoderConfig)
	}
	//其他开发测试环境，使用对人类友好的控制台格式输出
	return zapcore.NewConsoleEncoder(encoderConfig)

}

func getLogWriter(fileName string) zapcore.WriteSyncer {
	//file, _ := os.Create("./test.log")
	//return zapcore.AddSync(file)

	//启用滚动日志，日志自动切割及归档
	lumberJackLogger := &lumberjack.Logger{
		Filename:   fileName,
		MaxSize:    300, // megabytes
		MaxBackups: 999,
		MaxAge:     365,  // days
		LocalTime:  true, // 采用当地时间
		Compress:   false,
	}
	return zapcore.AddSync(lumberJackLogger)
}

// NewZapLogger 返回 *zap.Logger 日志对象
func NewZapLogger(env string, fileName string, atomLevel zap.AtomicLevel) *zap.Logger {
	var allCore []zapcore.Core

	encoder := getEncoder(env)
	fileWriteSyncer := getLogWriter(fileName)     //输出到文件
	consoleWriteSyncer := zapcore.Lock(os.Stdout) //输出到控制台

	if env == "dev" { //开发环境同时输出日志到控制台和文件，便于快速调试
		allCore = append(allCore, zapcore.NewCore(encoder, consoleWriteSyncer, atomLevel))
	}
	allCore = append(allCore, zapcore.NewCore(encoder, fileWriteSyncer, atomLevel))

	//core := zapcore.NewCore(encoder, fileWriteSyncer, atomLevel) //第三个参数确定日志输出级别
	core := zapcore.NewTee(allCore...)

	return zap.New(core, zap.AddCaller()) //AddCaller 打印文件名、行号
}
