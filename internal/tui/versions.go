package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"treadle/internal/live"
)

// What production runs, webhooks, schedules and the API, is the published version of
// a flow, not its draft. p publishes the draft with a note, and v lists the versions:
// when and by whom each was kept, what it changed, and which one is live. From there
// an older version can go back into the draft, for everyone editing it, or be
// published again.

const (
	editorOverlayPublish  editorOverlay = "publish"
	editorOverlayVersions editorOverlay = "versions"

	editorActionPublish  editorAction = "publish"
	editorActionVersions editorAction = "versions"
)

type versionsLoadedMsg struct {
	flowID   int
	versions []live.Version
	err      error
}

type publishedMsg struct {
	version live.Version
	err     error
}

type restoredMsg struct {
	number int
	err    error
}

func (m *model) openPublish() {
	m.closeEditorOverlay()
	m.editorOverlay = editorOverlayPublish
	m.publishVersion = 0
	m.publishInput.SetValue("")
	m.publishInput.Focus()
}

// openPublishVersion asks for a note before publishing an older version again.
func (m *model) openPublishVersion(version live.Version) {
	m.editorOverlay = editorOverlayPublish
	m.publishVersion = version.ID
	m.publishVersionNumber = version.Number
	m.publishInput.SetValue("")
	m.publishInput.Focus()
}

func (m model) updatePublishKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.publishInput.Blur()
		if m.publishVersion != 0 {
			m.editorOverlay = editorOverlayVersions
			return m, nil
		}
		m.closeEditorOverlay()
		return m, nil
	}
	if msg.Code == tea.KeyEnter {
		note := strings.TrimSpace(m.publishInput.Value())
		m.publishInput.Blur()
		m.closeEditorOverlay()
		return m, m.publish(m.publishVersion, note)
	}
	var cmd tea.Cmd
	m.publishInput, cmd = m.publishInput.Update(msg)
	return m, cmd
}

// publish publishes the draft, once what is still here reached the server, or an
// older version.
func (m *model) publish(versionID int, note string) tea.Cmd {
	if versionID == 0 && m.liveActive() && (m.liveSending || m.liveUnsent()) {
		m.publishAfterSend, m.publishNote = true, note
		m.liveSendQueued = false
		return m.sendNow()
	}
	m.editorStatusMessage = "Publishing..."
	client, flowID := m.liveClient(), m.editorWorkflowID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		var version live.Version
		var err error
		if versionID == 0 {
			version, err = client.Publish(ctx, flowID, note)
		} else {
			version, err = client.PublishVersion(ctx, flowID, versionID, note)
		}
		return publishedMsg{version: version, err: err}
	}
}

func (m model) handlePublished(msg publishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.editorStatusMessage = "Can't publish: " + truncatePlain(msg.err.Error(), 60)
		return m, nil
	}
	m.editorStatusMessage = fmt.Sprintf("Published version %d: production runs it now", msg.version.Number)
	m.appendActivity(fmt.Sprintf("Published %s, version %d", m.editorWorkflowName, msg.version.Number))
	if m.editorOverlay == editorOverlayVersions {
		return m, m.loadVersions()
	}
	return m, nil
}

func (m *model) openVersions() tea.Cmd {
	m.closeEditorOverlay()
	m.editorOverlay = editorOverlayVersions
	m.versionSelected = 0
	m.versionConfirm = ""
	return m.loadVersions()
}

func (m *model) loadVersions() tea.Cmd {
	m.versionsLoading = true
	m.versionsError = ""
	client, flowID := m.liveClient(), m.editorWorkflowID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		versions, err := client.Versions(ctx, flowID)
		return versionsLoadedMsg{flowID: flowID, versions: versions, err: err}
	}
}

func (m model) handleVersionsLoaded(msg versionsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.flowID != m.editorWorkflowID {
		return m, nil
	}
	m.versionsLoading = false
	if msg.err != nil {
		m.versionsError = msg.err.Error()
		return m, nil
	}
	sort.SliceStable(msg.versions, func(i, j int) bool { return msg.versions[i].Number > msg.versions[j].Number })
	m.versions = msg.versions
	m.versionSelected = clamp(m.versionSelected, 0, max(len(m.versions)-1, 0))
	return m, nil
}

func (m model) selectedVersion() (live.Version, bool) {
	if m.versionSelected < 0 || m.versionSelected >= len(m.versions) {
		return live.Version{}, false
	}
	return m.versions[m.versionSelected], true
}

func (m model) updateVersionsKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if m.versionConfirm != "" {
		switch msg.String() {
		case "y", "enter":
			action := m.versionConfirm
			m.versionConfirm = ""
			if version, ok := m.selectedVersion(); ok && action == "restore" {
				return m, m.restoreVersion(version)
			}
		case "n", "esc":
			m.versionConfirm = ""
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "v", "q":
		m.closeEditorOverlay()
	case "up", "k":
		m.versionSelected = max(m.versionSelected-1, 0)
	case "down", "j":
		m.versionSelected = min(m.versionSelected+1, max(len(m.versions)-1, 0))
	case "r":
		if _, ok := m.selectedVersion(); ok {
			m.versionConfirm = "restore"
		}
	case "p":
		if version, ok := m.selectedVersion(); ok && !version.Live {
			m.openPublishVersion(version)
		}
	case "ctrl+r":
		return m, m.loadVersions()
	}
	return m, nil
}

func (m *model) restoreVersion(version live.Version) tea.Cmd {
	m.editorStatusMessage = fmt.Sprintf("Restoring version %d...", version.Number)
	client, flowID := m.liveClient(), m.editorWorkflowID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		return restoredMsg{number: version.Number, err: client.Restore(ctx, flowID, version.ID)}
	}
}

func (m model) handleRestored(msg restoredMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.editorStatusMessage = "Can't restore: " + truncatePlain(msg.err.Error(), 60)
		return m, nil
	}
	m.closeEditorOverlay()
	m.editorStatusMessage = fmt.Sprintf("Version %d is the draft now. Publish it to run it in production.", msg.number)
	// Live, the draft comes back through the stream like any change. Otherwise it is read again.
	if !m.liveActive() {
		return m, reloadAfterAgentCmd(m.activeHost, m.activeAPIKey, m.editorWorkflowID)
	}
	return m, nil
}

func versionKind(kind string) string {
	switch kind {
	case "publish":
		return "Published"
	case "run":
		return "Before a run"
	case "checkpoint":
		return "Checkpoint"
	}
	return kind
}

// versionChanges says what a version changed from the one before it.
func versionChanges(changes map[string]int) string {
	order := []struct{ key, noun string }{
		{"nodes_added", "added"}, {"nodes_removed", "removed"}, {"nodes_changed", "changed"},
		{"nodes_moved", "moved"}, {"connections_added", "connections added"},
		{"connections_removed", "connections removed"}, {"connections_reordered", "connections reordered"},
		{"comments_changed", "notes changed"},
	}
	var parts []string
	for _, item := range order {
		count, ok := changes[item.key]
		if !ok || count == 0 {
			continue
		}
		if strings.HasPrefix(item.key, "nodes_") {
			parts = append(parts, fmt.Sprintf("%s %s", plural(count, "node"), item.noun))
		} else if item.key == "connections_reordered" {
			parts = append(parts, item.noun)
		} else {
			parts = append(parts, fmt.Sprintf("%d %s", count, item.noun))
		}
	}
	if len(parts) == 0 {
		return "No changes from the one before"
	}
	return strings.Join(parts, ", ")
}

func (m model) versionsRect() (int, int) {
	return min(96, max(m.width-6, 40)), max(m.height-4, 12)
}

func (m model) renderVersions() string {
	width, height := m.versionsRect()
	inner := width - 4
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)

	var lines []string
	switch {
	case m.versionsLoading && len(m.versions) == 0:
		lines = append(lines, muted.Render("Loading..."))
	case m.versionsError != "":
		lines = append(lines, lipgloss.NewStyle().Foreground(errorColor).Render("Can't list the versions: "+m.versionsError))
	case len(m.versions) == 0:
		lines = append(lines, muted.Render("No versions yet. p publishes the draft as the first one."))
	}

	rows := max(height-9, 3)
	start := clamp(m.versionSelected-rows/2, 0, max(len(m.versions)-rows, 0))
	for i := start; i < min(start+rows, len(m.versions)); i++ {
		lines = append(lines, m.versionRow(m.versions[i], inner, i == m.versionSelected))
	}

	lines = append(lines, "")
	if version, ok := m.selectedVersion(); ok {
		detail := versionChanges(version.Changes)
		if names := contributorNames(version.Contributors); names != "" {
			detail += ". By " + names
		}
		lines = append(lines, muted.Render(truncatePlain(detail, inner)))
	} else {
		lines = append(lines, "")
	}

	hint := "↑↓ choose  r restore into the draft  p publish this one  esc close"
	if m.versionConfirm == "restore" {
		version, _ := m.selectedVersion()
		hint = fmt.Sprintf("Put version %d in the draft, for everyone editing it?  y yes  n no", version.Number)
		lines = append(lines, lipgloss.NewStyle().Foreground(warmColor).Bold(true).Render(truncatePlain(hint, inner)))
	} else {
		lines = append(lines, muted.Render(truncatePlain(hint, inner)))
	}

	panel := renderSectionPanel("Versions of "+truncatePlain(m.editorWorkflowName, 40), lines, width, min(height, len(lines)+3))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

func (m model) versionRow(version live.Version, width int, selected bool) string {
	author := ""
	if version.Author != nil {
		author = version.Author.Name()
	}
	label := fmt.Sprintf("%-5s %-13s %-15s %-12s %s",
		fmt.Sprintf("v%d", version.Number), versionKind(version.Kind),
		relativeTime(version.InsertedAt), truncatePlain(author, 12), version.Note)
	marker, color := "·", mutedTextColor
	if version.Live {
		marker, color = "●", successColor
	}
	row := renderListRow(marker, color, truncatePlain(label, width-12), width-8, selected)
	if version.Live {
		row += lipgloss.NewStyle().Foreground(successColor).Render(" live")
	}
	return row
}

func contributorNames(people []live.Author) string {
	names := make([]string, 0, len(people))
	for _, person := range people {
		names = append(names, person.Name())
	}
	return strings.Join(names, ", ")
}

func (m model) renderPublish() string {
	width := min(64, max(m.width-8, 32))
	title, intro := "Publish", "Production starts running the draft as it is now."
	if m.publishVersion != 0 {
		title = fmt.Sprintf("Publish version %d", m.publishVersionNumber)
		intro = "Production goes back to running this version."
	}
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)
	lines := []string{
		muted.Render(intro),
		"",
		"Note, to say what changed:",
		lipgloss.NewStyle().Foreground(accentColor).Render("› ") + m.publishInput.View(),
		"",
		muted.Render("⏎ publish  esc cancel"),
	}
	panel := renderSectionPanel(title, lines, width, len(lines)+3)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}
