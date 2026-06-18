package logger

import (
	"go.uber.org/zap"
)

func InitializeStartupLogger() {
	if err := MustInitializeFromEnv(); err != nil {
		Log.Error("failed to initialize application logger", zap.Error(err))
	}
}

func ExitOnError(msg string, err error) {
	if err == nil {
		return
	}
	Log.Error(msg, zap.Error(err))
	_ = Sync()
}
