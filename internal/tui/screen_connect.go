package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"treadle"
)

const (
	connectFullHeight  = 30
	connectRoomyHeight = 20
	connectFieldHeight = 3
)

func (m model) updateConnect(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.Code == tea.KeyEnter && !m.connecting && !m.oauthWaiting {
			if m.browserButtonFocused {
				return m.startOAuth()
			}
			return m.startConnection()
		}
		if msg.String() == "tab" || msg.String() == "shift+tab" || msg.String() == "up" || msg.String() == "down" {
			return m.focusNextConnectControl(msg.String() == "shift+tab" || msg.String() == "up"), nil
		}
	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft {
			return m.handleConnectClick(mouse.X, mouse.Y)
		}
	}

	var cmd1, cmd2 tea.Cmd
	m.hostInput, cmd1 = m.hostInput.Update(msg)
	m.apiKeyInput, cmd2 = m.apiKeyInput.Update(msg)
	return m, tea.Batch(cmd1, cmd2)
}

func (m model) View() tea.View {
	if m.width <= 0 || m.height <= 0 {
		return baseView("")
	}

	if m.width < minWidth || m.height < minHeight {
		body := lipgloss.NewStyle().
			Foreground(mutedTextColor).
			Render("Increase terminal size to use Treadle.")
		frame := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
		return baseView(frame)
	}

	switch m.screen {
	case screenWorkflowEditor:
		view := baseView(m.renderWorkflowEditor())
		view.Cursor = m.terminalCursor()
		return view
	case screenApp:
		return baseView(m.renderAppScreen())
	default:
		return baseView(m.renderConnectScreen())
	}
}

func (m model) handleConnectionResult(msg connectionResultMsg) (tea.Model, tea.Cmd) {
	m.connecting = false

	if msg.err != nil {
		m.connected = false
		m.connectError = describeDialError(m.hostInput.Value(), msg.err)
		return m, m.hostInput.Focus()
	}

	m.connected = true
	m.activeHost = msg.host
	m.activeAPIKey = msg.apiKey

	m.connectError = ""
	m.screen = screenApp
	m.loadingWorkflows = true
	m.workflowsError = ""
	m.hostInput.Blur()
	m.apiKeyInput.Blur()
	m.composerInput.Blur()
	m.appendActivity(fmt.Sprintf("Connected to %s.", msg.host))

	return m, tea.Batch(m.spinner.Tick, loadWorkflowsCmd(msg.host, msg.apiKey))
}

func (m model) handleConnectClick(x, y int) (model, tea.Cmd) {
	switch {
	case inside(x, y, m.connectLayout.host):
		m.connectButtonFocused = false
		m.apiKeyInput.Blur()
		return m, m.hostInput.Focus()
	case inside(x, y, m.connectLayout.key):
		m.connectButtonFocused = false
		m.hostInput.Blur()
		return m, m.apiKeyInput.Focus()
	case inside(x, y, m.connectLayout.button):
		if m.connecting || m.oauthWaiting {
			return m, nil
		}
		return m.startConnection()
	case inside(x, y, m.connectLayout.browser):
		if m.connecting || m.oauthWaiting {
			return m, nil
		}
		return m.startOAuth()
	default:
		return m, nil
	}
}

func (m model) focusNextConnectControl(reverse bool) model {
	const controls = 4

	focusIndex := 0
	switch {
	case m.apiKeyInput.Focused():
		focusIndex = 1
	case m.connectButtonFocused:
		focusIndex = 2
	case m.browserButtonFocused:
		focusIndex = 3
	}

	if reverse {
		focusIndex = (focusIndex + controls - 1) % controls
	} else {
		focusIndex = (focusIndex + 1) % controls
	}

	m.hostInput.Blur()
	m.apiKeyInput.Blur()
	m.connectButtonFocused = false
	m.browserButtonFocused = false

	switch focusIndex {
	case 0:
		m.hostInput.Focus()
	case 1:
		m.apiKeyInput.Focus()
	case 2:
		m.connectButtonFocused = true
	case 3:
		m.browserButtonFocused = true
	}

	return m
}

// connectLayout places the login form. Rendering and mouse handling both read
// it, so what is drawn and what is clickable can't drift apart.
type connectLayout struct {
	compact   bool
	lockup    brandLockup
	lockupY   int
	titleY    int
	subtitleY int
	host      rect
	key       rect
	button    rect
	browser   rect
	statusY   int
}

func newConnectLayout(width, height int) connectLayout {
	l := connectLayout{compact: height < connectFullHeight, lockup: fullLockup, subtitleY: -1}
	titleGap, fieldGap, buttonHeight, statusGap := 1, 1, 3, 1
	if l.compact {
		l.lockup = smallLockup
		titleGap, fieldGap, buttonHeight, statusGap = 0, 0, 1, 0
		if height >= connectRoomyHeight {
			fieldGap, statusGap = 1, 1
		}
	}

	formWidth := min(connectFormWidth, max(width-8, 1))
	formX := max((width-formWidth)/2, 0)

	y := l.lockup.rows() + titleGap
	l.titleY = y
	y++
	if !l.compact {
		l.subtitleY = y
		y++
	}
	y++

	l.host = rect{x: formX, y: y, w: formWidth, h: connectFieldHeight}
	y += connectFieldHeight + fieldGap
	l.key = rect{x: formX, y: y, w: formWidth, h: connectFieldHeight}
	y += connectFieldHeight + 1
	l.button = rect{x: formX, y: y, w: formWidth, h: buttonHeight}
	y += buttonHeight
	l.browser = rect{x: formX, y: y, w: formWidth, h: 1}
	y += 1 + statusGap
	l.statusY = y

	return l.centered(max(height-footerHeight, 1))
}

// centered moves the form down so it sits in the middle of the body.
func (l connectLayout) centered(bodyHeight int) connectLayout {
	offset := max((bodyHeight-l.statusY-1)/2, 0)

	l.lockupY += offset
	l.titleY += offset
	if l.subtitleY >= 0 {
		l.subtitleY += offset
	}
	l.host.y += offset
	l.key.y += offset
	l.button.y += offset
	l.browser.y += offset
	l.statusY += offset
	return l
}

func (m model) connectThreadState() threadState {
	switch {
	case m.connecting, m.oauthWaiting:
		return threadConnecting
	case m.connectError != "":
		return threadFailed
	default:
		return threadIdle
	}
}

// describeDialError keeps the end of a dial error, the part that explains it.
func describeDialError(host string, err error) string {
	reason := err.Error()
	if i := strings.LastIndex(reason, ": "); i >= 0 {
		reason = reason[i+2:]
	}
	return "Can't reach " + strings.TrimSpace(host) + ": " + reason
}

func (m model) renderConnectStatus(width int) string {
	style := lipgloss.NewStyle().Foreground(mutedTextColor)
	switch {
	case m.oauthNotice != "":
		return style.Foreground(warmColor).Render(truncatePlain(m.oauthNotice, width))
	case m.oauthWaiting:
		return style.Foreground(accentColor).Render("Finish signing in in your browser. esc cancels.")
	case m.connecting:
		return style.Foreground(accentColor).Render("Reaching " + truncatePlain(m.hostInput.Value(), max(width-12, 8)) + "...")
	case m.connectError != "":
		return style.Foreground(errorColor).Render(truncatePlain(m.connectError, width))
	case strings.TrimSpace(m.apiKeyInput.Value()) == "":
		return style.Render("Paste an API key, or sign in with your browser.")
	default:
		return style.Render("Ready to connect.")
	}
}

func (m model) renderConnectScreen() string {
	l := m.connectLayout
	rows := make([]string, max(m.height-footerHeight, 1))
	put := func(y, x int, lines ...string) {
		for i, line := range lines {
			if y+i >= 0 && y+i < len(rows) {
				rows[y+i] = strings.Repeat(" ", max(x, 0)) + line
			}
		}
	}
	center := func(y int, line string) {
		put(y, (m.width-lipgloss.Width(line))/2, line)
	}

	put(l.lockupY, (m.width-l.lockup.width())/2, l.lockup.render(m.connectThreadState(), m.threadFrame)...)

	center(l.titleY, lipgloss.NewStyle().Bold(true).Foreground(textColor).Render("T R E A D L E"))
	if !l.compact {
		center(l.subtitleY, lipgloss.NewStyle().Foreground(mutedTextColor).Render("Connect to your Heddle server"))
	}

	put(l.host.y, l.host.x, renderField("HOST", m.hostInput.View(), l.host.w, m.hostInput.Focused())...)
	put(l.key.y, l.key.x, renderField("API KEY", m.apiKeyInput.View(), l.key.w, m.apiKeyInput.Focused())...)

	label := "Connect  ⏎"
	if m.connecting {
		label = m.spinner.View() + " Connecting"
	}
	put(l.button.y, l.button.x, renderButton(label, l.button.w, m.connectButtonFocused, m.connecting, l.compact)...)

	browser := lipgloss.NewStyle().Foreground(mutedTextColor)
	browserLabel := "Sign in with browser"
	if m.browserButtonFocused {
		browser = browser.Foreground(accentColor).Bold(true)
		browserLabel = "› " + browserLabel + " ‹"
	}
	center(l.browser.y, browser.Render(browserLabel))

	center(l.statusY, m.renderConnectStatus(l.host.w))

	version := "v" + treadle.Version + "  "
	footer := renderHints("tab next  ⏎ connect  esc quit", m.footerRect.w-lipgloss.Width(version)) +
		lipgloss.NewStyle().Foreground(mutedTextColor).Render(version)
	return strings.Join(rows, "\n") + "\n" + footer
}
