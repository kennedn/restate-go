package logging

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
func Log(level string, message string, args ...any) {
	_log(level, message, args...)
}

func _log(level string, message string, args ...any) {
	if currentLevel >= level {
		_, file, line, ok := runtime.Caller(2)
		if ok {
			_, filename := filepath.Split(file)
			timestamp := time.Now().Format("2006-01-02 15:04:05.999")
			message = fmt.Sprintf(message, args...)
			message = fmt.Sprintf("[%s][%s:%d][%s]\t%s", timestamp, filename, line, level, message)
		}
		logger.Println(message)
	}

}

func NginxLog(level string, method string, url string, request *http.Request, response *http.Response) {
	urlSlice := strings.Split(url, "/")
	referer := request.Referer()
	if referer == "" {
		referer = "-"
	}
	_log(Info, "%s %s \"%s %s %s\" %d \"%s\" \"%s\"", urlSlice[2], "", method, strings.Join(urlSlice[3:], "/"), request.Proto, response.StatusCode, referer, request.UserAgent())
}

type StatusRecorder struct {
	http.ResponseWriter
	StatusCode int
}

func (r *StatusRecorder) WriteHeader(statusCode int) {
	r.StatusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func RequestLogger(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &StatusRecorder{
			ResponseWriter: w,
			StatusCode:     0,
		}

		h.ServeHTTP(recorder, r)
		clientIP := r.Header.Get("X-Forwarded-For")
		if clientIP == "" {
			clientIP = strings.Split(r.RemoteAddr, ":")[0]
		}
		method := r.Method
		user := r.URL.User.Username()
		path := r.URL.RequestURI()
		status := recorder.StatusCode
		referer := r.Referer()
		if referer == "" {
			referer = "-"
		}
		userAgent := r.UserAgent()
		_log(Info, "%s %s \"%s %s %s\" %d \"%s\" \"%s\"", clientIP, user, method, path, r.Proto, status, referer, userAgent)
	})
}
