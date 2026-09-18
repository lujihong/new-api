package logger

import (
	"io"
	"log"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// These tests are intentionally serial: they replace the package's global
// writers and log directory. No production threshold or stdout is changed.
func loggerTestState(t *testing.T, directory string) {
	t.Helper()
	oldDir := common.LogDir
	common.LogDir = &directory
	oldCount := logCount.Load()
	common.LogWriterMu.Lock()
	oldWriter, oldErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	common.LogWriterMu.Unlock()
	currentLogPathMu.Lock()
	oldPath, oldFile := currentLogPath, currentLogFile
	currentLogPath, currentLogFile = "", nil
	currentLogPathMu.Unlock()
	oldLogWriter := log.Writer()
	log.SetOutput(io.Discard)
	logCount.Store(0)
	t.Cleanup(func() {
		// Every test must join its callers and wait for its rotation before
		// restoring globals. This mutex also synchronizes file cleanup.
		setupLogLock.Lock()
		defer setupLogLock.Unlock()
		common.LogWriterMu.Lock()
		defer common.LogWriterMu.Unlock()
		currentLogPathMu.Lock()
		if currentLogFile != nil {
			_ = currentLogFile.Close()
		}
		currentLogPath, currentLogFile = oldPath, oldFile
		currentLogPathMu.Unlock()
		gin.DefaultWriter, gin.DefaultErrorWriter = oldWriter, oldErrorWriter
		common.LogDir = oldDir
		logCount.Store(oldCount)
		log.SetOutput(oldLogWriter)
	})
}

func waitForLogger(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for logger")
		}
		runtime.Gosched()
	}
}

func TestSetupLoggerNonOwnerPreservesRotation(t *testing.T) {
	for _, directory := range []string{"", t.TempDir()} {
		t.Run(map[bool]string{true: "disabled", false: "file"}[directory == ""], func(t *testing.T) {
			loggerTestState(t, directory)
			// Represent a scheduled rotation that owns the gate and setup lock.
			setupLogWorking.Store(true)
			setupLogLock.Lock()
			var callers sync.WaitGroup
			for i := 0; i < 16; i++ {
				callers.Add(1)
				go func() { defer callers.Done(); SetupLogger() }()
			}
			callers.Wait()
			if !setupLogWorking.Load() {
				t.Error("non-owner SetupLogger cleared the active rotation gate")
			}
			setupLogLock.Unlock()
			setupLogWorking.Store(false)
		})
	}
}

func TestLogRotationThreshold(t *testing.T) {
	loggerTestState(t, "")
	logCount.Store(maxLogCount - 1)
	logHelper(nil, loggerINFO, "at threshold")
	if got := logCount.Load(); got != maxLogCount {
		t.Fatalf("exact threshold must not rotate: count=%d", got)
	}
	if setupLogWorking.Load() {
		t.Fatal("exact threshold acquired rotation gate")
	}
	logHelper(nil, loggerWarn, "over threshold")
	waitForLogger(t, func() bool { return !setupLogWorking.Load() })
	if got := logCount.Load(); got != 0 {
		t.Fatalf("threshold+1 must reset counter: count=%d", got)
	}
	// Disabled file logging must also release the gate for the next cycle.
	logCount.Store(maxLogCount)
	logHelper(nil, loggerError, "second cycle")
	waitForLogger(t, func() bool { return !setupLogWorking.Load() })
	if got := logCount.Load(); got != 0 {
		t.Fatalf("second cycle did not rotate: count=%d", got)
	}
}

func TestSetupLoggerActiveOwnerAndConcurrentLogs(t *testing.T) {
	loggerTestState(t, t.TempDir())
	// Hold writer switching so the real owner remains active while contenders
	// run. No sleeps or million-line log flood are needed.
	common.LogWriterMu.RLock()
	ownerDone := make(chan struct{})
	go func() { defer close(ownerDone); SetupLogger() }()
	waitForLogger(t, func() bool { return GetCurrentLogPath() != "" })
	var callers sync.WaitGroup
	for i := 0; i < 16; i++ {
		callers.Add(1)
		go func() { defer callers.Done(); SetupLogger() }()
	}
	callers.Wait()
	if !setupLogWorking.Load() {
		t.Error("active direct SetupLogger must own the rotation gate")
	}
	common.LogWriterMu.RUnlock()
	<-ownerDone
	if setupLogWorking.Load() {
		t.Error("completed owner did not release the gate")
	}

	// No-file setup leaves our discard writer in place; exercise many direct
	// setup attempts concurrently with one threshold crossing.
	*common.LogDir = ""
	common.LogWriterMu.Lock()
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	common.LogWriterMu.Unlock()
	logCount.Store(maxLogCount)
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			for j := 0; j < 8; j++ {
				SetupLogger()
				logHelper(nil, loggerINFO, "concurrent rotation")
			}
		}()
	}
	close(start)
	callers.Wait()
	waitForLogger(t, func() bool { return !setupLogWorking.Load() })
	// If all threshold-crossing callers lost to direct setup, the next log
	// retries rotation rather than leaving the gate stuck.
	logHelper(nil, loggerINFO, "drain")
	waitForLogger(t, func() bool { return !setupLogWorking.Load() })
	if got := logCount.Load(); got < 0 || got > 257 {
		t.Errorf("counter not reset after concurrent rotation: %d", got)
	}
	if _, err := os.Stat(GetCurrentLogPath()); err != nil {
		t.Errorf("direct startup did not create log file: %v", err)
	}
}
