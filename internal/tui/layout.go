package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type rect struct {
	x int
	y int
	w int
	h int
}

func inside(x, y int, r rect) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

func (m *model) reflow() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	footerY := max(m.height-footerHeight, 0)
	m.footerRect = rect{x: 0, y: footerY, w: m.width, h: footerHeight}

	m.connectLayout = newConnectLayout(m.width, m.height)

	bodyHeight := max(m.height-appHeaderHeight-composerHeight-footerHeight, 1)
	sidebarWidth := clamp(max(m.width/4, minSidebarWidth), minSidebarWidth, maxSidebarWidth)
	if sidebarWidth > m.width-24-sectionGapSize {
		sidebarWidth = max(m.width/3, minSidebarWidth)
	}

	m.sidebarRect = rect{x: 0, y: appHeaderHeight, w: sidebarWidth, h: bodyHeight}
	m.mainRect = rect{
		x: sidebarWidth + sectionGapSize,
		y: appHeaderHeight,
		w: max(m.width-sidebarWidth-sectionGapSize, 1),
		h: bodyHeight,
	}
	m.commandRect = rect{x: 0, y: appHeaderHeight + bodyHeight, w: m.width, h: composerHeight}

	editorTopBarHeight := 2
	editorBodyY := editorTopBarHeight
	editorBodyHeight := max(m.height-footerHeight-editorTopBarHeight-editorBottomRuleHeight, 1)
	editorSidebarWidth := clamp(max(m.width/5, 22), 20, 30)
	canvasWidth := max(m.width-editorSidebarWidth-sectionGapSize, 1)

	chatWidth := 0
	if m.aiChatOpen {
		chatWidth = clamp(m.width/3, minChatWidth, maxChatWidth)
		if canvasWidth-chatWidth < minCanvasWidthWithChat {
			chatWidth = 0
		}
	}
	canvasWidth -= chatWidth

	m.paletteRect = rect{x: 0, y: editorBodyY, w: editorSidebarWidth, h: editorBodyHeight}
	m.canvasRect = rect{x: editorSidebarWidth + sectionGapSize, y: editorBodyY, w: canvasWidth, h: editorBodyHeight}
	m.inspectorRect = rect{x: 0, y: 0, w: 0, h: 0}
	m.aiChatRect = rect{}
	m.aiPromptInputRect = rect{}
	if chatWidth > 0 {
		m.aiChatRect = rect{x: m.width - chatWidth, y: editorBodyY, w: chatWidth, h: editorBodyHeight}
		m.aiPromptInputRect = rect{
			x: m.aiChatRect.x + 2,
			y: m.aiChatRect.y + editorBodyHeight - aiChatInputHeight - 1,
			w: chatWidth - 4,
			h: aiChatInputHeight,
		}
	}

	m.workflowListRect = rect{
		x: m.sidebarRect.x,
		y: m.sidebarRect.y + workflowListTop,
		w: m.sidebarRect.w,
		h: max(m.sidebarRect.h-workflowListTop, 1),
	}
	m.commandInputRect = rect{
		x: m.commandRect.x + 2,
		y: m.commandRect.y + 1,
		w: max(m.commandRect.w-4, 1),
		h: 1,
	}

	m.hostInput.SetWidth(max(m.connectLayout.host.w-1, 0))
	m.hostInput.CharLimit = max(m.connectLayout.host.w*4, 32)
	m.apiKeyInput.SetWidth(max(m.connectLayout.key.w-1, 0))
	m.apiKeyInput.CharLimit = max(m.connectLayout.key.w*4, 128)
	m.composerInput.SetWidth(max(m.commandInputRect.w-3, 0))
	m.composerInput.CharLimit = max(m.commandInputRect.w*4, 64)
	m.aiPromptInput.SetWidth(max(m.aiPromptInputRect.w-3-lipgloss.Width(m.aiPromptInput.Prompt), 0))
	m.aiPromptInput.CharLimit = max(m.aiPromptInputRect.w*6, 128)
	m.aiChatScroll = clampScroll(m.aiChatScroll, m.aiChatLineCount(), m.aiChatVisibleLines())

	m.workflowScroll = clampScroll(m.workflowScroll, len(m.workflows)+1, max(m.workflowListRect.h, 1))
	m.rebuildNodeRects()
}

func renderSectionPanel(title string, lines []string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	innerWidth := max(width-4, 1)
	contentHeight := max(height-2, 0)
	border := lipgloss.NewStyle().Foreground(panelBorder).Render
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(textColor)

	contentLines := append([]string{titleStyle.Render(title)}, lines...)
	fitted := fitLines(contentLines, innerWidth, contentHeight)

	var b strings.Builder
	b.WriteString(border("╭" + strings.Repeat("─", max(width-2, 0)) + "╮"))
	for _, line := range fitted {
		b.WriteByte('\n')
		b.WriteString(border("│ ") + padRightBlank(line, innerWidth) + border(" │"))
	}
	b.WriteByte('\n')
	b.WriteString(border("╰" + strings.Repeat("─", max(width-2, 0)) + "╯"))
	return b.String()
}
