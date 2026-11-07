package tui

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.reflow()
	case tea.FocusMsg:
		m.terminalFocused = true
	case tea.BlurMsg:
		m.terminalFocused = false
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			// In the editor it copies, like everywhere else. With nothing to copy it
			// still quits, so there's always a way out.
			if !m.editorCanCopy() {
				return m, tea.Quit
			}
		case "q":
			if !m.textInputFocused() {
				return m, tea.Quit
			}
		case "esc":
			if m.screen == screenWorkflowEditor {
				next, cmd := m.updateWorkflowEditor(msg)
				return next, cmd
			}
			if m.screen == screenApp {
				if m.composerInput.Focused() {
					m.composerInput.Blur()
				}
				return m, nil
			}
			if m.oauthWaiting {
				return m.cancelOAuth(), m.hostInput.Focus()
			}
			return m, tea.Quit
		}
	case connectionResultMsg:
		return m.handleConnectionResult(msg)
	case oauthStartedMsg:
		return m.handleOAuthStarted(msg)
	case oauthFinishedMsg:
		return m.handleOAuthFinished(msg)
	case workflowsLoadedMsg:
		return m.handleWorkflowsLoaded(msg)
	case workflowLoadedMsg:
		return m.handleWorkflowLoaded(msg)
	case workflowSavedMsg:
		return m.handleWorkflowSaved(msg)
	case agentStreamMsg:
		return m.handleAgentStream(msg)
	case agentSentMsg:
		return m.handleAgentSent(msg)
	case agentFlowLoadedMsg:
		return m.handleAgentFlowLoaded(msg)
	case executionStartedMsg:
		return m.handleExecutionStarted(msg)
	case executionPolledMsg:
		return m.handleExecutionPolled(msg)
	}

	var cmds []tea.Cmd

	switch m.screen {
	case screenConnect:
		next, cmd := m.updateConnect(msg)
		m = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case screenApp:
		next, cmd := m.updateApp(msg)
		m = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case screenWorkflowEditor:
		next, cmd := m.updateWorkflowEditor(msg)
		m = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if m.connecting || m.loadingWorkflows || m.oauthWaiting {
		if _, ok := msg.(spinner.TickMsg); ok {
			m.threadFrame++
		}

		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m model) textInputFocused() bool {
	return m.hostInput.Focused() || m.apiKeyInput.Focused() || m.composerInput.Focused() ||
		m.aiPromptInput.Focused() || m.editorMode == editorModeModal
}

func (m model) editorCanCopy() bool {
	return m.screen == screenWorkflowEditor && m.editorMode == editorModeNormal &&
		m.editorOverlay == editorOverlayNone && m.canCopySelection()
}
