package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "treadle"
	keyringSession = "session"

	loginAPIKey = "api_key"
	loginOAuth  = "oauth"
)

// savedSession is what lets Treadle reconnect without asking again.
type savedSession struct {
	Host   string `json:"host"`
	Token  string `json:"token"`
	Method string `json:"method"`
}

func credentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "treadle", "credentials.json"), nil
}

// loadSession prefers the system keyring and falls back to the credentials file.
func loadSession() (savedSession, bool) {
	if raw, err := keyring.Get(keyringService, keyringSession); err == nil {
		if session, ok := parseSession([]byte(raw)); ok {
			return session, true
		}
	}

	if path, err := credentialsPath(); err == nil {
		if raw, err := os.ReadFile(path); err == nil {
			if session, ok := parseSession(raw); ok {
				return session, true
			}
		}
	}

	return loadLegacySession()
}

func parseSession(raw []byte) (savedSession, bool) {
	var session savedSession
	if err := json.Unmarshal(raw, &session); err != nil || session.Host == "" || session.Token == "" {
		return savedSession{}, false
	}
	return session, true
}

// loadLegacySession reads what older versions kept in the keyring.
func loadLegacySession() (savedSession, bool) {
	host, err := keyring.Get(keyringService, "host")
	if err != nil || host == "" {
		return savedSession{}, false
	}
	token, err := keyring.Get(keyringService, "api_key")
	if err != nil || token == "" {
		return savedSession{}, false
	}
	return savedSession{Host: host, Token: token, Method: loginAPIKey}, true
}

// saveSession needs no keyring to work: many Linux desktops don't run a Secret
// Service, so the file, readable only by this user, is the fallback.
func saveSession(session savedSession) error {
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}

	if err := keyring.Set(keyringService, keyringSession, string(raw)); err == nil {
		removeCredentialsFile()
		return nil
	}
	return writeCredentialsFile(raw)
}

func writeCredentialsFile(raw []byte) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	// CreateTemp opens with 0600, so the token is never readable by others.
	file, err := os.CreateTemp(dir, "credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func removeCredentialsFile() {
	if path, err := credentialsPath(); err == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
	}
}

func clearSession() {
	_ = keyring.Delete(keyringService, keyringSession)
	_ = keyring.Delete(keyringService, "host")
	_ = keyring.Delete(keyringService, "api_key")
	removeCredentialsFile()
}
