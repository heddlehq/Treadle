package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	backgroundColor    = lipgloss.Color("#222222")
	inputBackground    = lipgloss.Color("#333333")
	panelBorder        = lipgloss.Color("#615fff")
	shadowColor        = lipgloss.Color("#222222")
	focusedBorderColor = lipgloss.Color("#615fff")
	shadowEdgeColor    = lipgloss.Color("#222222")
	textColor          = lipgloss.Color("#F3F4F6")
	mutedTextColor     = lipgloss.Color("#9CA3AF")
	accentColor        = lipgloss.Color("#60A5FA")
	successColor       = lipgloss.Color("#34D399")
	errorColor         = lipgloss.Color("#F87171")
)

func textInputStyles() textinput.Styles {
	styles := textinput.DefaultDarkStyles()
	base := lipgloss.NewStyle().
		Foreground(textColor).
		Background(inputBackground)
	muted := lipgloss.NewStyle().
		Foreground(mutedTextColor).
		Background(inputBackground)

	styles.Focused.Text = base
	styles.Focused.Prompt = base
	styles.Focused.Placeholder = muted
	styles.Focused.Suggestion = muted

	blurredBase := lipgloss.NewStyle().
		Foreground(mutedTextColor).
		Background(inputBackground)

	styles.Blurred.Text = blurredBase
	styles.Blurred.Prompt = blurredBase
	styles.Blurred.Placeholder = blurredBase
	styles.Blurred.Suggestion = blurredBase
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

func renderFooter(text string, width int) string {
	style := lipgloss.NewStyle().Foreground(mutedTextColor)
	return padRightBlank(style.Render(truncatePlain(text, max(width, 1))), max(width, 1))
}

func renderButton(label string, width int, focused bool) string {
	if width <= 0 {
		return ""
	}

	label = truncatePlain(ansi.Strip(label), width)
	style := lipgloss.NewStyle().
		Foreground(focusedBorderColor).
		Underline(true).
		Width(width).
		Align(lipgloss.Center)
	if focused {
		style = style.
			Foreground(textColor).
			Background(focusedBorderColor)
	}

	return style.Render(label)
}

func renderStatusValue(value string) string {
	style := lipgloss.NewStyle().Foreground(textColor)
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active":
		style = style.Foreground(successColor)
	case "running":
		style = style.Foreground(accentColor)
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

	bgStyle := lipgloss.NewStyle()

	b := lipgloss.RoundedBorder()
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}

	top := borderStyle.Render(b.TopLeft + strings.Repeat(b.Top, innerWidth) + b.TopRight)
	bottom := borderStyle.Render(b.BottomLeft + strings.Repeat(b.Bottom, innerWidth) + b.BottomRight)

	leftPad := bgStyle.Render(" ")
	availWidth := innerWidth - 1
	if availWidth < 0 {
		availWidth = 0
	}

	paddedVal := lipgloss.NewStyle().
		Background(inputBackground).
		Inline(true).
		MaxWidth(availWidth).
		Width(availWidth).
		Render(val)

	midLeft := borderStyle.Render(b.Left) + leftPad
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
