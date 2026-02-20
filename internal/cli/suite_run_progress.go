package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type suiteRunProgressEvent struct {
	V          int            `json:"v"`
	TS         string         `json:"ts"`
	Kind       string         `json:"kind"`
	RunID      string         `json:"runId,omitempty"`
	SuiteID    string         `json:"suiteId,omitempty"`
	MissionID  string         `json:"missionId,omitempty"`
	AttemptID  string         `json:"attemptId,omitempty"`
	Mode       string         `json:"mode,omitempty"`
	OutRoot    string         `json:"outRoot,omitempty"`
	OutDir     string         `json:"outDir,omitempty"`
	CampaignID string         `json:"campaignId,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

type suiteRunProgressEmitter struct {
	mu     sync.Mutex
	path   string
	stderr io.Writer
}

func newSuiteRunProgressEmitter(path string, stderr io.Writer) (*suiteRunProgressEmitter, error) {
	path = filepath.Clean(path)
	if path == "." || path == "" {
		return nil, nil
	}
	if path == "-" {
		return &suiteRunProgressEmitter{path: "-", stderr: stderr}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &suiteRunProgressEmitter{path: path, stderr: stderr}, nil
}

func (e *suiteRunProgressEmitter) Emit(ev suiteRunProgressEvent) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	ev.V = 1
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		return err
	}
	if e.path == "-" {
		if e.stderr == nil {
			return nil
		}
		_, err := e.stderr.Write(buf.Bytes())
		return err
	}
	f, err := os.OpenFile(e.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(buf.Bytes()); err != nil {
		return err
	}
	return f.Sync()
}

func (e *suiteRunProgressEmitter) Close() error {
	// No persistent handle is kept; close is present for call-site symmetry.
	return nil
}
