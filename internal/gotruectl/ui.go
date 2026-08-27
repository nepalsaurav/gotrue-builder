package gotruectl

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// Styling is applied only to human-facing status/table output. `key`'s
// token and `admin`'s JSON are deliberately left completely uncolored —
// they're meant to be piped or captured (export KEY=$(gotruectl key ...),
// | jq), and an embedded ANSI escape code would corrupt that. lipgloss
// itself also auto-detects a non-TTY/NO_COLOR environment and degrades to
// plain text, so piping any of the styled output below is safe too.
var (
	colorSuccess = lipgloss.Color("42")  // green
	colorError   = lipgloss.Color("196") // red
	colorWarn    = lipgloss.Color("214") // amber
	colorAccent  = lipgloss.Color("39")  // blue
	colorMuted   = lipgloss.Color("245") // gray

	successStyle = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	warnStyle    = lipgloss.NewStyle().Foreground(colorWarn)
	headerStyle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	mutedStyle   = lipgloss.NewStyle().Foreground(colorMuted)
	cellStyle    = lipgloss.NewStyle().Padding(0, 1)
)

func printSuccess(format string, a ...any) {
	fmt.Println(successStyle.Render(fmt.Sprintf(format, a...)))
}

func printWarn(format string, a ...any) {
	fmt.Println(warnStyle.Render(fmt.Sprintf(format, a...)))
}

func printError(format string, a ...any) {
	fmt.Println(errorStyle.Render(fmt.Sprintf(format, a...)))
}

func printMuted(format string, a ...any) {
	fmt.Println(mutedStyle.Render(fmt.Sprintf(format, a...)))
}

// renderTable renders a bordered, colored table for terminal output
// (falling back to plain text automatically on non-TTY output — piped,
// redirected to a file, NO_COLOR set — via lipgloss's own detection).
func renderTable(headers []string, rows [][]string) string {
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(mutedStyle).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle.Padding(0, 1)
			}
			return cellStyle
		})
	return t.Render()
}
