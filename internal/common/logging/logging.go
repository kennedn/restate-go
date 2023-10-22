package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// Log levels
const (
	Info  = "INFO"
	Error = "ERROR"
)

var (
	logger       = log.New(os.Stdout, "", 0)
	currentLevel = Info
)

// SetLogLevel sets the current log level.
func SetLogLevel(level string) {
	currentLevel = level
}

// Log logs a message with file and line number information at the specified level.
func Log(level, message string) {
	if currentLevel == level {
		_, file, line, ok := runtime.Caller(1)
		if ok {
			_, filename := filepath.Split(file)
			message = fmt.Sprintf("[%s %s:%d] %s", level, filename, line, message)
		}
		logger.Println(message)
	}
}
