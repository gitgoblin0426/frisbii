package frisbii

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var _ http.Handler = (*LogMiddleware)(nil)
var _ ErrorLogger = (*LoggingResponseWriter)(nil)

// LogMiddlware is a middleware that logs requests to the given io.Writer.
// it wraps requests in a LoggingResponseWriter that can be used to log
// standardised messages to the writer.
type LogMiddleware struct {
	next      http.Handler
	logWriter io.Writer
}

func NewLogMiddleware(next http.Handler, logWriter io.Writer) *LogMiddleware {
	return &LogMiddleware{
		next:      next,
		logWriter: logWriter,
	}
}

func (lm *LogMiddleware) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	lres := NewLoggingResponseWriter(res, req, lm.logWriter)
	start := time.Now()
	defer func() {
		lres.Log(lres.status, time.Since(start), lres.sentBytes, lres.CompressionRatio(), "")
	}()
	lm.next.ServeHTTP(lres, req)
}

var _ http.ResponseWriter = (*LoggingResponseWriter)(nil)

type LoggingResponseWriter struct {
	http.ResponseWriter
	logWriter  io.Writer
	req        *http.Request
	status     int
	wroteBytes int
	sentBytes  int
	wrote      bool
}

func NewLoggingResponseWriter(w http.ResponseWriter, req *http.Request, logWriter io.Writer) *LoggingResponseWriter {
	return &LoggingResponseWriter{
		ResponseWriter: w,
		req:            req,
		logWriter:      logWriter,
	}
}

func (w *LoggingResponseWriter) CompressionRatio() string {
	if w.sentBytes == 0 || w.wroteBytes == 0 || w.wroteBytes == w.sentBytes {
		return "-"
	}
	s := fmt.Sprintf("%.2f", float64(w.wroteBytes)/float64(w.sentBytes))
	if s == "0.00" {
		return "-"
	}
	return s
}

func (w *LoggingResponseWriter) Log(status int, duration time.Duration, bytes int, CompressionRatio string, msg string) {
	if w.wrote {
		return
	}
	w.wrote = true
	remoteAddr := w.req.RemoteAddr
	if ss := strings.Split(remoteAddr, ":"); len(ss) > 0 {
		remoteAddr = ss[0]
	}
	fmt.Fprintf(
		w.logWriter,
		"%s %s %s \"%s\" %d %d %d %s %s %s\n",
		time.Now().Format(time.RFC3339),
		remoteAddr,
		w.req.Method,
		w.req.URL,
		status,
		duration.Milliseconds(),
		bytes,
		CompressionRatio,
		strconv.Quote(w.req.UserAgent()),
		strconv.Quote(msg),
	)
}

func (w *LoggingResponseWriter) LogError(status int, err error) {
	msg := err.Error()
	// unwrap error and find the msg at the bottom error
	for {
		if e := errors.Unwrap(err); e != nil {
			msg = e.Error()
			err = e
		} else {
			break
		}
	}
	w.Log(status, 0, 0, "-", msg)
	w.status = status
	if w.sentBytes == 0 {
		http.Error(w.ResponseWriter, strconv.Quote(msg), status)
	}
}

func (w *LoggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *LoggingResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.sentBytes += n
	return n, err
}

func (w *LoggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("http.Hijacker not implemented")
}
