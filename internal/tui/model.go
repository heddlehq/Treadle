package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"treadle/internal/agent"
	"treadle/internal/live"
)

const (
	defaultServer          = "localhost:4000"
	connectTimeout         = 15 * time.Second
	minWidth               = 72
	minHeight              = 16
	minSidebarWidth        = 28
	maxSidebarWidth        = 36
	composerHeight         = 2
	appHeaderHeight        = 2
	editorBottomRuleHeight = 1
	footerHeight           = 1
	sectionGapSize         = 1
	panelPaddingX          = 2
	panelPaddingTop        = 1
	panelPaddingBottom     = 1
	connectFormWidth       = 46
	defaultWorkflowName    = "Untitled workflow"
)

type screen string

const (
	screenConnect        screen = "connect"
	screenApp            screen = "app"
	screenWorkflowEditor screen = "workflow_editor"
)

type workflow struct {
	id          int
	name        string
	status      string
	updated     string
	created     string
	nodes       int
	connections int
}

type editorMode string

const (
	editorModeNormal  editorMode = "normal"
	editorModeAdd     editorMode = "add"
	editorModeConnect editorMode = "connect"
	editorModeModal   editorMode = "modal"
)

type editorOverlay string

const (
	editorOverlayNone    editorOverlay = ""
	editorOverlayContext editorOverlay = "context"
	editorOverlayCommand editorOverlay = "command"
	editorOverlayCreate  editorOverlay = "create"
	editorOverlayResult  editorOverlay = "result"
	editorOverlayHelp    editorOverlay = "help"
	editorOverlayGoTo    editorOverlay = "go_to"
)

type editorAction string

const (
	editorActionOpenCreate editorAction = "open_create"
	editorActionCreate     editorAction = "create"
	editorActionDelete     editorAction = "delete"
	editorActionEdit       editorAction = "edit"
	editorActionConnect    editorAction = "connect"
	editorActionBack       editorAction = "back"
	editorActionAddComment editorAction = "add_comment"
	editorActionUndo       editorAction = "undo"
	editorActionRedo       editorAction = "redo"
	editorActionCopy       editorAction = "copy"
	editorActionPaste      editorAction = "paste"
)

type editorMenuItem struct {
	label     string
	shortcut  string
	action    editorAction
	kind      string
	disabled  bool
	danger    bool
	separator bool
}

type editorNode struct {
	id       string
	kind     string
	label    string
	x        int
	y        int
	w        int
	h        int
	controls []nodeControl

	// Where the server had this node. Cells are coarser than pixels, so a node that
	// wasn't moved goes back exactly where it came from.
	loaded    bool
	loadedAt  canvasPoint
	loadedPos apiPosition

	// The size of a comment in the web editor, kept like loadedPos.
	pxWidth  float64
	pxHeight float64
}

type nodeControl struct {
	name    string
	label   string
	value   string
	kind    string
	options []controlOption

	// Saved with the node but not part of its form, like what the web editor keeps
	// for itself in every node.
	hidden bool
}

type editorConnection struct {
	source       string
	sourceOutput string
	target       string
	targetInput  string
}

type connectionResultMsg struct {
	host   string
	apiKey string
	err    error
}

type workflowsLoadedMsg struct {
	workflows []workflow
	nodes     []apiNodeDef
	err       error
}

type workflowLoadedMsg struct {
	flow apiFlow
	err  error
}

type workflowSavedMsg struct {
	flow apiFlow
	err  error
}

type model struct {
	screen screen

	hostInput     textinput.Model
	apiKeyInput   textinput.Model
	composerInput textinput.Model
	modalInput    textinput.Model
	aiPromptInput textinput.Model
	publishInput  textinput.Model
	spinner       spinner.Model

	width  int
	height int

	connecting           bool
	connected            bool
	connectButtonFocused bool
	browserButtonFocused bool
	oauthWaiting         bool
	oauthCancel          context.CancelFunc
	oauthNotice          string
	loadingWorkflows     bool
	threadFrame          int
	terminalFocused      bool

	activeHost     string
	activeAPIKey   string
	activeMethod   string
	savedSession   savedSession
	connectError   string
	workflowsError string

	workflows        []workflow
	availableNodes   []apiNodeDef
	selectedWorkflow int
	workflowScroll   int
	activities       []string
	commandOutput    []string

	editorWorkflowID     int
	editorWorkflowName   string
	editorNodes          []editorNode
	editorConnections    []editorConnection
	editorDirty          bool
	editorSaving         bool
	editorRunAfterSave   bool
	editorRunInFlight    bool
	editorExecution      *apiExecution
	editorPollFailures   int
	editorResultScroll   int
	editorResultFull     bool
	editorStatusMessage  string
	selectedNode         int
	draggingNode         int
	dragAnchorX          int
	dragAnchorY          int
	dragOrigins          map[string]canvasPoint
	extraSelection       map[string]bool
	selectedConnection   int
	undoStack            []editorSnapshot
	redoStack            []editorSnapshot
	clipboardNodes       []editorNode
	clipboardConnections []editorConnection
	pasteCount           int
	dragMoved            bool
	viewX                int
	viewY                int
	zoomIndex            int
	panning              bool
	panLastX             int
	panLastY             int
	marqueeActive        bool
	marqueeAdditive      bool
	marqueeStartX        int
	marqueeStartY        int
	marqueeX             int
	marqueeY             int
	connectSource        int
	connectOutput        int
	wireActive           bool
	wireX                int
	wireY                int
	paletteIndex         int
	paletteScroll        int
	editorMode           editorMode
	editorOverlay        editorOverlay
	editorMenuX          int
	editorMenuY          int
	editorMenuSelected   int
	paletteQuery         string
	helpScroll           int
	wireCursor           int
	wireNode             int
	editorMenuTarget     int
	editorCreateAtMenu   bool
	agentTranscript      agent.Transcript
	aiChatScroll         int
	aiChatOpen           bool
	agentUnread          bool
	agentFollowing       int
	agentStopFollowing   context.CancelFunc
	agentBusy            bool
	agentSending         bool
	agentWaitingForSave  bool
	agentPrompt          string
	agentQueuedPrompt    string
	modalFocusedControl  int
	terminal             *terminalSession
	live                 *liveSession
	liveGen              int
	liveState            liveState
	liveClientID         string
	liveCanEdit          bool
	liveSeq              int
	liveBase             workflowPayload
	liveBaseSnapshot     editorSnapshot
	livePending          *live.Graph
	livePendingSeq       int
	liveSending          bool
	liveSendQueued       bool
	autosaveHeld         bool
	publishVersion       int
	publishVersionNumber int
	publishAfterSend     bool
	publishNote          string
	versions             []live.Version
	versionsLoading      bool
	versionsError        string
	versionSelected      int
	versionConfirm       string
	liveFocusQueued      bool
	liveFocusSent        string
	liveUndo             []liveDelta
	liveRedo             []liveDelta
	livePeople           []live.Person
	liveCursors          map[string]liveCursor
	terminalOpen         bool
	terminalFocus        bool
	terminalWide         bool
	terminalScroll       int
	modalDraft           []nodeControl
	nodeRects            map[string]rect

	connectLayout     connectLayout
	sidebarRect       rect
	workflowListRect  rect
	mainRect          rect
	commandRect       rect
	commandInputRect  rect
	canvasRect        rect
	aiChatRect        rect
	aiPromptInputRect rect
	paletteRect       rect
	inspectorRect     rect
	terminalRect      rect
	footerRect        rect
}

func initialModel() model {
	hostInput := textinput.New()
	hostInput.SetValue(defaultServer)
	hostInput.SetWidth(0)
	hostInput.CharLimit = len(defaultServer) + 64
	hostInput.Placeholder = "localhost:4000"
	hostInput.Prompt = ""
	hostInput.SetVirtualCursor(true)
	hostInput.SetStyles(textInputStyles())
	hostInput.Focus()

	apiKeyInput := textinput.New()
	apiKeyInput.SetWidth(0)
	apiKeyInput.CharLimit = 128
	apiKeyInput.Placeholder = "hd_live_..."
	apiKeyInput.Prompt = ""
	apiKeyInput.SetVirtualCursor(true)
	apiKeyInput.SetStyles(textInputStyles())
	apiKeyInput.EchoMode = textinput.EchoPassword
	apiKeyInput.EchoCharacter = '•'

	composerInput := textinput.New()
	composerInput.SetWidth(0)
	composerInput.CharLimit = 256
	composerInput.Placeholder = "Ask the agent about the selected workflow..."
	composerInput.Prompt = ""
	composerInput.SetVirtualCursor(true)
	composerInput.SetStyles(textInputStyles())

	modalInput := textinput.New()
	modalInput.SetWidth(0)
	modalInput.Prompt = ""
	modalInput.SetVirtualCursor(true)
	modalInput.SetStyles(textInputStyles())

	aiPromptInput := textinput.New()
	aiPromptInput.SetWidth(0)
	aiPromptInput.CharLimit = 512
	aiPromptInput.Placeholder = "Ask the agent to build or change this flow..."
	aiPromptInput.Prompt = ""
	aiPromptInput.SetVirtualCursor(true)
	aiPromptInput.SetStyles(textInputStyles())

	publishInput := textinput.New()
	publishInput.SetWidth(48)
	publishInput.CharLimit = 200
	publishInput.Placeholder = "What changed, for whoever reads the versions"
	publishInput.Prompt = ""
	publishInput.SetVirtualCursor(true)
	publishInput.SetStyles(textInputStyles())

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accentColor)

	session, hasSession := loadSession()
	if hasSession {
		hostInput.SetValue(session.Host)
		if session.Method != loginOAuth {
			apiKeyInput.SetValue(session.Token)
		}
		hostInput.Blur()
	}

	return model{
		savedSession:       session,
		connecting:         hasSession,
		activeMethod:       fallbackString(session.Method, loginAPIKey),
		screen:             screenConnect,
		hostInput:          hostInput,
		apiKeyInput:        apiKeyInput,
		composerInput:      composerInput,
		modalInput:         modalInput,
		aiPromptInput:      aiPromptInput,
		publishInput:       publishInput,
		spinner:            sp,
		terminalFocused:    true,
		selectedNode:       -1,
		selectedConnection: -1,
		zoomIndex:          defaultZoomIndex,
		draggingNode:       -1,
		connectSource:      -1,
		editorMenuTarget:   -1,
		editorMode:         editorModeNormal,
		nodeRects:          map[string]rect{},
	}
}

// Init reconnects straight away when a login was saved.
func (m model) Init() tea.Cmd {
	if m.savedSession.Token == "" {
		return textinput.Blink
	}
	return tea.Batch(textinput.Blink, m.spinner.Tick, connectCmd(m.savedSession.Host, m.savedSession.Token))
}
