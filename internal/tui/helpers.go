package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func wrapPlainPreserveStyle(line string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	if lipgloss.Width(line) <= width {
		return []string{line}
	}

	var out []string
	remaining := line
	for lipgloss.Width(remaining) > width {
		var b strings.Builder
		for _, r := range remaining {
			next := b.String() + string(r)
			if lipgloss.Width(next) > width {
				break
			}
			b.WriteRune(r)
		}
		chunk := b.String()
		if chunk == "" {
			break
		}
		out = append(out, chunk)
		remaining = strings.TrimPrefix(remaining, chunk)
	}
	if remaining != "" {
		out = append(out, remaining)
	}
	if len(out) == 0 {
		return []string{truncatePlain(line, width)}
	}
	return out
}

func fitLines(lines []string, width, height int) []string {
	if height <= 0 {
		return nil
	}

	out := make([]string, 0, height)
	for _, line := range lines {
		for _, wrapped := range wrapPlainPreserveStyle(line, width) {
			out = append(out, wrapped)
			if len(out) == height {
				return out
			}
		}
	}

	for len(out) < height {
		out = append(out, "")
	}
	return out
}

func padRightBlank(value string, width int) string {
	valueWidth := lipgloss.Width(value)
	if valueWidth >= width {
		return value
	}
	return value + blank(width-valueWidth)
}

func blank(width int) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat("\u00a0", width)
}

func truncatePlain(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= maxWidth {
		return value
	}

	var b strings.Builder
	for _, r := range value {
		next := b.String() + string(r)
		if lipgloss.Width(next+"...") > maxWidth {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "..."
}

func centeredLine(value string, width int) string {
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, value)
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}

	diff := time.Since(t)
	if diff < 0 {
		diff = 0
	}

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(diff.Hours()))
	case diff < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", int(diff.Hours()/24))
	}
}

func clampScroll(offset, total, height int) int {
	maxOffset := max(total-height, 0)
	if offset < 0 {
		return 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *model) appendActivity(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	m.activities = append(m.activities, line)
}

func mapIconToTerminal(iconName string) string {
	iconName = strings.ToLower(iconName)
	switch {
	case strings.Contains(iconName, "play") || strings.Contains(iconName, "start"):
		return "▶"
	case strings.Contains(iconName, "link") || strings.Contains(iconName, "hook"):
		return "↗"
	case strings.Contains(iconName, "variable") || strings.Contains(iconName, "var"):
		return "x"
	case strings.Contains(iconName, "code") || strings.Contains(iconName, "bracket"):
		return "ƒ"
	case strings.Contains(iconName, "globe") || strings.Contains(iconName, "http") || strings.Contains(iconName, "web"):
		return "◉"
	case strings.Contains(iconName, "arrow") || strings.Contains(iconName, "condition") || strings.Contains(iconName, "switch"):
		return "↔"
	case strings.Contains(iconName, "chat") || strings.Contains(iconName, "log") || strings.Contains(iconName, "message"):
		return "≡"
	case strings.Contains(iconName, "check") || strings.Contains(iconName, "circle") || strings.Contains(iconName, "output"):
		return "✓"
	case strings.Contains(iconName, "db") || strings.Contains(iconName, "database"):
		return "◆"
	case strings.Contains(iconName, "mail") || strings.Contains(iconName, "email"):
		return "»"
	case strings.Contains(iconName, "file") || strings.Contains(iconName, "document"):
		return "▪"
	case strings.Contains(iconName, "key") || strings.Contains(iconName, "lock"):
		return "◇"
	case strings.Contains(iconName, "user") || strings.Contains(iconName, "person"):
		return "○"
	case strings.Contains(iconName, "time") || strings.Contains(iconName, "clock") || strings.Contains(iconName, "wait"):
		return "▸"
	case strings.Contains(iconName, "trash") || strings.Contains(iconName, "delete"):
		return "✕"
	case strings.Contains(iconName, "stop") || strings.Contains(iconName, "end"):
		return "■"
	default:
		return "▪"
	}
}
