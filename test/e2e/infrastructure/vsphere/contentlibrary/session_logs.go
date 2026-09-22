// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package contentlibrary

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
)

// NewSessionLogs observes download sessions for a single library item.
func NewSessionLogs(itemID string) *SessionLogs {
	return &SessionLogs{itemID: itemID, created: map[string]bool{}, preparing: map[string]bool{}, downloaded: map[string]bool{}}
}

var (
	sessionIDPattern = regexp.MustCompile(`(?:"sessionID"\s*:\s*"|sessionID=")([^"\s]+)"`)
	itemIDPattern    = regexp.MustCompile(`(?:"itemID"\s*:\s*"|itemID=")([^"\s]+)"`)
)

// SessionLogs watches existing V(4) logs without sending any requests to
// the download session itself. A restarted pod or lost stream fails the test.
type SessionLogs struct {
	mu                             sync.Mutex
	itemID                         string
	created, preparing, downloaded map[string]bool
	err                            error
}

// Read records session events until the stream ends. An unexpected EOF is an error.
func (l *SessionLogs) Read(ctx context.Context, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		item := itemIDPattern.FindStringSubmatch(line)
		if len(item) != 2 || item[1] != l.itemID {
			continue
		}
		match := sessionIDPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		l.mu.Lock()
		switch {
		case strings.Contains(line, "download session for item created"):
			l.created[match[1]] = true
		case strings.Contains(line, "request posted to prepare file"):
			l.preparing[match[1]] = true
		case strings.Contains(line, "Downloaded file"):
			l.downloaded[match[1]] = true
		}
		l.mu.Unlock()
	}
	if ctx.Err() == nil {
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		l.mu.Lock()
		l.err = fmt.Errorf("operator log stream ended before the test completed: %w", err)
		l.mu.Unlock()
	}
}

// Check requires exactly one session to have started preparation, and optionally
// requires that same session to have completed the download.
func (l *SessionLogs) Check(completed bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	if len(l.created) != 1 || len(l.preparing) != 1 {
		return fmt.Errorf("expected one operator download session preparing item %s, got created=%v preparing=%v", l.itemID, l.created, l.preparing)
	}
	for sessionID := range l.created {
		if !l.preparing[sessionID] {
			return fmt.Errorf("preparation did not use the original session %s", sessionID)
		}
		if completed && (len(l.downloaded) != 1 || !l.downloaded[sessionID]) {
			return fmt.Errorf("download has not completed using original session %s", sessionID)
		}
	}
	return nil
}

// HasDownload reports whether any session has completed file preparation.
func (l *SessionLogs) HasDownload() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.downloaded) != 0
}
