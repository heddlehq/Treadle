package tui

import (
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/zalando/go-keyring"
)

const (
	defaultServer       = "localhost:4000"
	connectTimeout      = 15 * time.Second
	minWidth            = 72
	minHeight           = 16
	minSidebarWidth     = 28
	maxSidebarWidth     = 36
	composerHeight      = 5
	footerHeight        = 1
	sectionGapSize      = 1
	panelPaddingX       = 2
	panelPaddingTop     = 1
	panelPaddingBottom  = 1
	connectCardWidth    = 56
	connectCardHeight   = 19
	connectInputWidth   = 34
	connectButtonWidth  = 14
	defaultWorkflowName = "Untitled workflow"
)

type screen string

const (
	screenConnect        screen = "connect"
	screenApp            screen = "app"
	screenWorkflowEditor screen = "workflow_editor"
)

type workflow struct {
	id         int
	name       string
	status     string
	lastRun    string
	nodes      int
	recentRuns []string
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
)

type editorAction string

const (
	editorActionOpenCreate editorAction = "open_create"
	editorActionCreate     editorAction = "create"
	editorActionDelete     editorAction = "delete"
	editorActionEdit       editorAction = "edit"
	editorActionConnect    editorAction = "connect"
	editorActionBack       editorAction = "back"
)

type editorMenuItem struct {
	label  string
	action editorAction
	kind   string
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
}

type nodeControl struct {
	name  string
	label string
	value string
}

type editorConnection struct {
	source string
	target string
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
	aiPromptInput textinput.Model
	spinner       spinner.Model

	width  int
	height int

	connecting           bool
	connected            bool
	connectButtonFocused bool
	loadingWorkflows     bool
	terminalFocused      bool

	activeHost     string
	activeAPIKey   string
	connectStatus  string
	connectError   string
	workflowsError string

	workflows        []workflow
	availableNodes   []apiNodeDef
	selectedWorkflow int
	workflowScroll   int
	activities       []string
	commandOutput    []string

	editorWorkflowID    int
	editorWorkflowName  string
	editorNodes         []editorNode
	editorConnections   []editorConnection
	editorDirty         bool
	editorSaving        bool
	editorStatusMessage string
	selectedNode        int
	draggingNode        int
	dragOffsetX         int
	dragOffsetY         int
	connectSource       int
	paletteIndex        int
	editorMode          editorMode
	editorOverlay       editorOverlay
	editorMenuX         int
	editorMenuY         int
	editorMenuSelected  int
	editorMenuTarget    int
	editorCreateAtMenu  bool
	aiChatMessages      []string
	aiChatScroll        int
	modalFocusedControl int
	modalDraft          []nodeControl
	nodeRects           map[string]rect

	connectCardRect    rect
	connectInputRect   rect
	connectButtonRect  rect
	sidebarRect        rect
	navRect            rect
	workflowListRect   rect
	mainRect           rect
	commandRect        rect
	commandInputRect   rect
	canvasRect         rect
	aiChatRect         rect
	aiChatMessagesRect rect
	aiPromptInputRect  rect
	paletteRect        rect
	inspectorRect      rect
	modalRect          rect
	footerRect         rect
}

func initialModel() model {
	hostInput := textinput.New()
	hostInput.SetValue(defaultServer)
	hostInput.SetWidth(0)
	hostInput.CharLimit = len(defaultServer) + 64
	hostInput.Placeholder = "localhost:4000"
	hostInput.Prompt = "Host: "
	hostInput.SetVirtualCursor(true)
	hostInput.SetStyles(textInputStyles())
	hostInput.Focus()

	if savedHost, err := keyring.Get("treadle", "host"); err == nil && savedHost != "" {
		hostInput.SetValue(savedHost)
	}

	apiKeyInput := textinput.New()
	apiKeyInput.SetWidth(0)
	apiKeyInput.CharLimit = 128
	apiKeyInput.Placeholder = "hd_live_..."
	apiKeyInput.Prompt = "Key:  "
	apiKeyInput.SetVirtualCursor(true)
	apiKeyInput.SetStyles(textInputStyles())
	apiKeyInput.EchoMode = textinput.EchoPassword
	apiKeyInput.EchoCharacter = '•'

	if savedKey, err := keyring.Get("treadle", "api_key"); err == nil && savedKey != "" {
		apiKeyInput.SetValue(savedKey)
	}

	composerInput := textinput.New()
	composerInput.SetWidth(0)
	composerInput.CharLimit = 256
	composerInput.Placeholder = "Type a command, search workflow, or ask the agent..."
	composerInput.Prompt = ""
	composerInput.SetVirtualCursor(true)
	composerInput.SetStyles(textInputStyles())

	aiPromptInput := textinput.New()
	aiPromptInput.SetWidth(0)
	aiPromptInput.CharLimit = 512
	aiPromptInput.Placeholder = "Describe the workflow to build..."
	aiPromptInput.Prompt = ""
	aiPromptInput.SetVirtualCursor(true)
	aiPromptInput.SetStyles(textInputStyles())

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accentColor)

	return model{
		screen:        screenConnect,
		hostInput:     hostInput,
		apiKeyInput:   apiKeyInput,
		composerInput: composerInput,
		aiPromptInput: aiPromptInput,
		spinner:       sp,
		connectStatus: "Enter server host to continue.",
		aiChatMessages: []string{
			"Agent: Olá! Descreva o workflow que deseja construir.",
			"You: Quero um workflow que processa CSV e salva no banco",
			"Agent: Entendido! Vou criar: Start → Evaluate Code → Variable → Output",
			"Agent: Adicionei um nó de leitura de CSV. Configure o caminho do arquivo no inspector.",
			"You: Adiciona também validação de dados",
			"Agent: Nó de validação adicionado entre o CSV e o banco. Use o inspector para definir as regras.",
		},
		terminalFocused:  true,
		selectedNode:     -1,
		draggingNode:     -1,
		connectSource:    -1,
		editorMenuTarget: -1,
		editorMode:       editorModeNormal,
		nodeRects:        map[string]rect{},
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}
