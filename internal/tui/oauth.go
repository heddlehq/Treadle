package tui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	oauthClientID = "treadle"
	oauthTimeout  = 5 * time.Minute
	oauthScopes   = "workflows:read workflows:write workflows:delete nodes:read executions:read executions:write"
)

var (
	errOAuthUnsupported = errors.New("this server can't do browser sign-in yet. Use an API key, or update Heddle")
	errOAuthDenied      = errors.New("sign-in was cancelled in the browser")
)

// openBrowser is a variable so tests can stand in for the person at the browser.
var openBrowser = func(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

type oauthMetadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

// oauthStartedMsg means the browser is on its way and the callback is listening.
type oauthStartedMsg struct {
	authorizeURL string
	browserErr   error
	cancel       context.CancelFunc
	finished     <-chan oauthFinishedMsg
}

type oauthFinishedMsg struct {
	host  string
	token string
	err   error
}

type oauthCallback struct {
	code string
	err  error
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func fetchOAuthMetadata(ctx context.Context, baseURL string) (oauthMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return oauthMetadata{}, err
	}

	client := &http.Client{Timeout: connectTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return oauthMetadata{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return oauthMetadata{}, errOAuthUnsupported
	}

	var metadata oauthMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return oauthMetadata{}, errOAuthUnsupported
	}
	if metadata.AuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
		return oauthMetadata{}, errOAuthUnsupported
	}
	return metadata, nil
}

// startOAuthCmd runs the authorization code flow with PKCE for native apps (RFC
// 8252): the browser comes back to a port on this machine that only exists for
// the length of the sign-in.
func startOAuthCmd(server string) tea.Cmd {
	return func() tea.Msg {
		baseURL, err := normalizeBaseURL(server)
		if err != nil {
			return oauthFinishedMsg{err: err}
		}

		metadataCtx, cancelMetadata := context.WithTimeout(context.Background(), connectTimeout)
		defer cancelMetadata()
		metadata, err := fetchOAuthMetadata(metadataCtx, baseURL)
		if err != nil {
			return oauthFinishedMsg{err: err}
		}

		verifier, err := randomToken()
		if err != nil {
			return oauthFinishedMsg{err: err}
		}
		state, err := randomToken()
		if err != nil {
			return oauthFinishedMsg{err: err}
		}

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return oauthFinishedMsg{err: err}
		}
		redirectURI := fmt.Sprintf("http://%s/callback", listener.Addr())

		callbacks := make(chan oauthCallback, 1)
		callbackServer := &http.Server{Handler: oauthCallbackHandler(state, callbacks), ReadHeaderTimeout: 5 * time.Second}
		go callbackServer.Serve(listener)

		query := url.Values{
			"response_type":         {"code"},
			"client_id":             {oauthClientID},
			"redirect_uri":          {redirectURI},
			"scope":                 {oauthScopes},
			"state":                 {state},
			"code_challenge":        {codeChallenge(verifier)},
			"code_challenge_method": {"S256"},
		}
		authorizeURL := metadata.AuthorizationEndpoint + "?" + query.Encode()
		browserErr := openBrowser(authorizeURL)

		ctx, cancel := context.WithTimeout(context.Background(), oauthTimeout)
		finished := make(chan oauthFinishedMsg, 1)
		go func() {
			defer cancel()
			defer callbackServer.Close()

			select {
			case <-ctx.Done():
				finished <- oauthFinishedMsg{err: ctx.Err()}
			case callback := <-callbacks:
				if callback.err != nil {
					finished <- oauthFinishedMsg{err: callback.err}
					return
				}
				token, err := exchangeOAuthCode(ctx, metadata.TokenEndpoint, callback.code, verifier, redirectURI)
				finished <- oauthFinishedMsg{host: baseURL, token: token, err: err}
			}
		}()

		return oauthStartedMsg{authorizeURL: authorizeURL, browserErr: browserErr, cancel: cancel, finished: finished}
	}
}

func waitForOAuthCmd(finished <-chan oauthFinishedMsg) tea.Cmd {
	return func() tea.Msg {
		return <-finished
	}
}

// oauthCallbackHandler ignores anything without the state it sent: other pages can
// reach a loopback port too.
func oauthCallbackHandler(state string, callbacks chan<- oauthCallback) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("state") != state {
			http.Error(w, "This sign-in link isn't valid.", http.StatusBadRequest)
			return
		}

		callback := oauthCallback{code: query.Get("code")}
		message := "You're signed in. You can close this tab and go back to Treadle."
		if oauthError := query.Get("error"); oauthError != "" || callback.code == "" {
			callback.err = errOAuthDenied
			if oauthError != "access_denied" {
				callback.err = fmt.Errorf("sign-in failed: %s", fallbackString(oauthError, "no code returned"))
			}
			message = "Sign-in didn't finish. You can close this tab and go back to Treadle."
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, oauthCallbackPage, message)

		select {
		case callbacks <- callback:
		default:
		}
	})
	return mux
}

const oauthCallbackPage = `<!doctype html><meta charset="utf-8"><title>Treadle</title>
<body style="background:#23262b;color:#f3ebdc;font:16px system-ui;display:grid;place-items:center;height:90vh">
<p>%s</p></body>`

func exchangeOAuthCode(ctx context.Context, tokenEndpoint, code, verifier, redirectURI string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"client_id":     {oauthClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: connectTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("token endpoint returned %s", resp.Status)
	}
	if resp.StatusCode != http.StatusOK || payload.AccessToken == "" {
		return "", fmt.Errorf("sign-in failed: %s", fallbackString(payload.Error, resp.Status))
	}
	return payload.AccessToken, nil
}

func (m model) startOAuth() (model, tea.Cmd) {
	server := strings.TrimSpace(m.hostInput.Value())
	if server == "" {
		server = defaultServer
		m.hostInput.SetValue(server)
	}

	m.oauthWaiting = true
	m.connected = false
	m.connectError = ""
	m.oauthNotice = ""
	m.hostInput.Blur()
	m.apiKeyInput.Blur()
	return m, tea.Batch(m.spinner.Tick, startOAuthCmd(server))
}

func (m model) handleOAuthStarted(msg oauthStartedMsg) (tea.Model, tea.Cmd) {
	m.oauthCancel = msg.cancel
	cmds := []tea.Cmd{waitForOAuthCmd(msg.finished)}
	if msg.browserErr != nil {
		m.oauthNotice = "Couldn't open a browser. The link is in your clipboard."
		cmds = append(cmds, tea.SetClipboard(msg.authorizeURL))
	}
	return m, tea.Batch(cmds...)
}

func (m model) cancelOAuth() model {
	if m.oauthCancel != nil {
		m.oauthCancel()
	}
	m.oauthWaiting = false
	m.oauthCancel = nil
	m.connectError = ""
	return m
}

func (m model) handleOAuthFinished(msg oauthFinishedMsg) (tea.Model, tea.Cmd) {
	wasWaiting := m.oauthWaiting
	m.oauthWaiting = false
	m.oauthCancel = nil
	m.oauthNotice = ""

	if !wasWaiting {
		return m, nil
	}
	if msg.err != nil {
		m.connectError = msg.err.Error()
		if errors.Is(msg.err, context.DeadlineExceeded) {
			m.connectError = "Sign-in timed out. Try again."
		}
		return m, m.hostInput.Focus()
	}

	m.activeMethod = loginOAuth
	return m.handleConnectionResult(connectionResultMsg{host: msg.host, apiKey: msg.token})
}
