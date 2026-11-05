package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The dark theme of the Heddle web app, from apps/heddle_ui/assets/css/app.css.
var (
	backgroundColor    = lipgloss.Color("#23262b")
	panelBorder        = lipgloss.Color("#4a4d54")
	shadowColor        = lipgloss.Color("#23262b")
	focusedBorderColor = lipgloss.Color("#e8907d")
	shadowEdgeColor    = lipgloss.Color("#23262b")
	textColor          = lipgloss.Color("#f3ebdc")
	mutedTextColor     = lipgloss.Color("#aca391")
	accentColor        = lipgloss.Color("#e8907d")
	onAccentColor      = lipgloss.Color("#181a1e")
	neutralColor       = lipgloss.Color("#33363c")
	threadDimColor     = lipgloss.Color("#5a5c60")
	canvasDotColor     = lipgloss.Color("#3a3d43")
	warmColor          = lipgloss.Color("#e8b77d")
	infoColor          = lipgloss.Color("#7db3bf")
	successColor       = lipgloss.Color("#2fb5a5")
	errorColor         = lipgloss.Color("#f0788a")
)

func textInputStyles() textinput.Styles {
	styles := textinput.DefaultDarkStyles()
	text := lipgloss.NewStyle().Foreground(textColor)
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)

	styles.Focused.Text = text
	styles.Focused.Prompt = text
	styles.Focused.Placeholder = muted
	styles.Focused.Suggestion = muted

	styles.Blurred.Text = text
	styles.Blurred.Prompt = muted
	styles.Blurred.Placeholder = muted
	styles.Blurred.Suggestion = muted
	styles.Cursor.Color = accentColor
	styles.Cursor.Blink = true

	return styles
}

func baseView(content string) tea.View {
	v := tea.NewView(content)
	v.BackgroundColor = backgroundColor
	v.ForegroundColor = textColor
	v.WindowTitle = "Treadle"
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	return v
}

func renderOptionalError(message string) string {
	if strings.TrimSpace(message) == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(errorColor).Render("Error: " + truncatePlain(message, 72))
}

// renderHints styles "key description" pairs separated by two spaces, keeping
// only the ones that fit whole.
func renderHints(text string, width int) string {
	key := lipgloss.NewStyle().Foreground(textColor).Render
	description := lipgloss.NewStyle().Foreground(mutedTextColor).Render

	const margin, gap = "  ", "   "
	row := margin
	used := lipgloss.Width(margin)
	for i, hint := range strings.Split(text, "  ") {
		needed := lipgloss.Width(hint)
		if i > 0 {
			needed += lipgloss.Width(gap)
		}
		if used+needed > width {
			break
		}
		if i > 0 {
			row += gap
		}
		name, rest, found := strings.Cut(hint, " ")
		row += key(name)
		if found {
			row += description(" " + rest)
		}
		used += needed
	}
	return padRightBlank(row, max(width, 1))
}

// renderButton returns one row when compact. Otherwise half blocks soften the
// top and bottom edges, making it three rows tall.
func renderButton(label string, width int, focused, busy, compact bool) []string {
	if width <= 0 {
		return nil
	}

	fill := accentColor
	ink := onAccentColor
	switch {
	case busy:
		fill = neutralColor
		ink = textColor
	case focused:
		fill = textColor
	}

	face := lipgloss.NewStyle().
		Bold(true).
		Foreground(ink).
		Background(fill).
		Width(width).
		Align(lipgloss.Center).
		Render(truncatePlain(ansi.Strip(label), width))
	if compact {
		return []string{face}
	}

	edge := lipgloss.NewStyle().Foreground(fill).Render
	return []string{edge(strings.Repeat("▄", width)), face, edge(strings.Repeat("▀", width))}
}

// renderField returns the three rows of a form field: label, value and rule.
func renderField(label, value string, width int, focused bool) []string {
	labelStyle := lipgloss.NewStyle().Foreground(mutedTextColor)
	rule := lipgloss.NewStyle().Foreground(panelBorder).Render(strings.Repeat("─", width))
	if focused {
		labelStyle = labelStyle.Foreground(accentColor).Bold(true)
		rule = lipgloss.NewStyle().Foreground(accentColor).Render(strings.Repeat("━", width))
	}
	return []string{labelStyle.Render(label), value, rule}
}

// categoryColor stands in for the node colours the API sends, which are Tailwind
// classes and mean nothing in a terminal.
func categoryColor(category string) color.Color {
	category = strings.ToLower(category)
	switch {
	case strings.Contains(category, "trigger"):
		return accentColor
	case strings.Contains(category, "flow"):
		return warmColor
	case strings.Contains(category, "data"):
		return successColor
	case strings.Contains(category, "code"), strings.Contains(category, "integration"):
		return infoColor
	default:
		return mutedTextColor
	}
}

func renderStatusValue(value string) string {
	style := lipgloss.NewStyle().Foreground(textColor)
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active":
		style = style.Foreground(successColor)
	case "running":
		style = style.Foreground(infoColor)
	case "failed":
		style = style.Foreground(errorColor)
	}
	return style.Render(value)
}

func renderShadowColumn(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	primaryWidth := max(width-1, 1)
	primary := lipgloss.NewStyle().
		Background(shadowColor).
		Render(strings.Repeat(" ", primaryWidth))
	edge := lipgloss.NewStyle().
		Background(shadowEdgeColor).
		Render(strings.Repeat(" ", width-primaryWidth))
	line := primary + edge

	lines := make([]string, height)
	for i := range lines {
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func renderShadowRow(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	primary := lipgloss.NewStyle().
		Background(shadowColor).
		Render(strings.Repeat(" ", width))
	edge := lipgloss.NewStyle().
		Background(shadowEdgeColor).
		Render(strings.Repeat(" ", width))

	lines := make([]string, height)
	for i := range lines {
		if i == height-1 {
			lines[i] = edge
			continue
		}
		lines[i] = primary
	}
	return strings.Join(lines, "\n")
}

func renderInputBlock(val string, width int, focused bool) string {
	borderColor := mutedTextColor
	if focused {
		borderColor = focusedBorderColor
	}
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)

	b := lipgloss.RoundedBorder()
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}

	top := borderStyle.Render(b.TopLeft + strings.Repeat(b.Top, innerWidth) + b.TopRight)
	bottom := borderStyle.Render(b.BottomLeft + strings.Repeat(b.Bottom, innerWidth) + b.BottomRight)

	availWidth := innerWidth - 1
	if availWidth < 0 {
		availWidth = 0
	}

	paddedVal := lipgloss.NewStyle().
		Inline(true).
		MaxWidth(availWidth).
		Width(availWidth).
		Render(val)

	midLeft := borderStyle.Render(b.Left) + " "
	midRight := borderStyle.Render(b.Right)

	middle := midLeft + paddedVal + midRight

	return top + "\n" + middle + "\n" + bottom
}

func panelLine(width int, content string, align lipgloss.Position) string {
	if width <= 0 {
		return ""
	}

	contentWidth := lipgloss.Width(content)
	if contentWidth > width {
		content = truncatePlain(content, width)
		contentWidth = lipgloss.Width(content)
	}

	left := 0
	switch align {
	case lipgloss.Center:
		left = max((width-contentWidth)/2, 0)
	case lipgloss.Right:
		left = max(width-contentWidth, 0)
	}

	right := max(width-left-contentWidth, 0)
	line := blank(left) + content + blank(right)
	return lipgloss.NewStyle().Foreground(textColor).Render(line)
}
