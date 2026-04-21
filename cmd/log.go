package cmd

import (
	"fmt"
	"os"
)

type logLevel int

const (
	levelError logLevel = iota
	levelWarn
	levelInfo
	levelDebug
)

var currentLevel = levelDebug

func setLogLevel() {
	switch {
	case quiet:
		currentLevel = levelError
	case verbose:
		currentLevel = levelDebug
	default:
		currentLevel = levelDebug
	}
}

func logErr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
}

func logWarn(format string, args ...any) {
	if currentLevel >= levelWarn {
		fmt.Printf("[WARN]  "+format+"\n", args...)
	}
}

func logInfo(format string, args ...any) {
	if currentLevel >= levelInfo {
		fmt.Printf("[INFO]  "+format+"\n", args...)
	}
}

func logDebug(format string, args ...any) {
	if currentLevel >= levelDebug {
		fmt.Printf("[DEBUG] "+format+"\n", args...)
	}
}

// logProgress emits a progress line. Always shows at info-level or above.
func logProgress(label string, done, total int64) {
	if currentLevel < levelInfo || total <= 0 {
		return
	}
	pct := float64(done) / float64(total) * 100
	fmt.Printf("[PROG]  %s: %d / %d (%.1f%%)\n", label, done, total, pct)
}
