package tui

import tea "charm.land/bubbletea/v2"

func (m model) handleAppClick(x, y int) (model, tea.Cmd) {
	switch {
	case inside(x, y, m.commandInputRect):
		return m, m.composerInput.Focus()
	case inside(x, y, m.workflowListRect):
		m.composerInput.Blur()
		row := y - m.workflowListRect.y
		index := m.workflowScroll + row
		switch {
		case index >= 0 && index < len(m.workflows):
			m.selectedWorkflow = index
			return m, nil
		case index == len(m.workflows):
			cmd := m.createWorkflow()
			return m, cmd
		}
	}

	return m, nil
}

func (m *model) handleAppScroll(mouse tea.Mouse) {
	if !inside(mouse.X, mouse.Y, m.workflowListRect) {
		return
	}

	switch mouse.Button {
	case tea.MouseWheelUp:
		m.workflowScroll = max(m.workflowScroll-1, 0)
	case tea.MouseWheelDown:
		m.workflowScroll = clampScroll(m.workflowScroll+1, len(m.workflows)+1, max(m.workflowListRect.h, 1))
	}
}
