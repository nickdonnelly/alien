package main

import (
	"fmt"
	"os"
)

// ANSI escape sequences. Kept inline (no library) so we have zero dependencies
// for the binary's runtime path.
const (
	cReset    = "\033[0m"
	cBold     = "\033[1m"
	cDim      = "\033[2m"
	cItalic   = "\033[3m"
	cStrike   = "\033[9m"
	cRed      = "\033[31m"
	cGreen    = "\033[32m"
	cYellow   = "\033[33m"
	cBlue     = "\033[34m"
	cMagenta  = "\033[35m"
	cCyan     = "\033[36m"
	cGray     = "\033[90m"
	cBrCyan   = "\033[96m"
	cBrGreen  = "\033[92m"
	cBrYellow = "\033[93m"
	cBrRed    = "\033[91m"
)

var useColor = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("ALIEN_FORCE_COLOR") != "" {
		return true
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func c(code, s string) string {
	if !useColor {
		return s
	}
	return code + s + cReset
}

func bold(s string) string    { return c(cBold, s) }
func dim(s string) string     { return c(cDim, s) }
func cyan(s string) string    { return c(cCyan, s) }
func brcyan(s string) string  { return c(cBrCyan, s) }
func green(s string) string   { return c(cGreen, s) }
func yellow(s string) string  { return c(cYellow, s) }
func red(s string) string     { return c(cRed, s) }
func magenta(s string) string { return c(cMagenta, s) }
func gray(s string) string    { return c(cGray, s) }

func successf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", green("✓"), fmt.Sprintf(format, args...))
}

func infof(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", brcyan("👽"), fmt.Sprintf(format, args...))
}

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", yellow("!"), fmt.Sprintf(format, args...))
}

func errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", red("✗"), fmt.Sprintf(format, args...))
}
