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

// logInline emits a debug-level message that overwrites the current line (no newline).
// Caller must invoke logInlineEnd() once the iterating loop completes.
func logInline(format string, args ...any) {
	if currentLevel < levelDebug {
		return
	}
	fmt.Printf("\r\033[K[DEBUG] "+format, args...)
}

// logInlineEnd terminates an in-place line started by logInline.
func logInlineEnd() {
	if currentLevel >= levelDebug {
		fmt.Println()
	}
}

// pct returns done/total as a percentage, guarding against division by zero.
func pct(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(done) / float64(total) * 100
}
