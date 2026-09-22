// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package contentlibrary provides content-library fixtures for E2E tests.
package contentlibrary

import (
	"context"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// Fixture serves an exported, static content library and can hold OVF GETs
// until Release. Metadata and HEAD requests remain available while held.
// A held response trickles bytes to avoid an idle HTTP connection timeout.
// Library manifests must use relative URLs so downloads reach this handler.
type Fixture struct {
	files   http.Handler
	mu      sync.Mutex
	release chan struct{}
	pending int
}

// NewFixture serves files from root. Requests are initially unblocked.
func NewFixture(root http.FileSystem) *Fixture {
	return &Fixture{files: http.FileServer(root)}
}

// Hold blocks subsequent OVF downloads. Calling Hold twice has no effect.
func (f *Fixture) Hold() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.release == nil {
		f.release = make(chan struct{})
	}
}

// Release allows current and subsequent downloads to complete.
func (f *Fixture) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.release != nil {
		close(f.release)
		f.release = nil
	}
}

// Pending reports the number of OVF requests currently held by the fixture.
func (f *Fixture) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending
}

// ServeHTTP serves metadata immediately and holds OVF GETs when armed.
func (f *Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.EqualFold(path.Ext(r.URL.Path), ".ovf") {
		f.mu.Lock()
		release := f.release
		if release != nil {
			f.pending++
		}
		f.mu.Unlock()
		if release != nil {
			defer func() {
				f.mu.Lock()
				f.pending--
				f.mu.Unlock()
			}()
			w = &heldResponseWriter{ResponseWriter: w, ctx: r.Context(), release: release}
		}
	}
	f.files.ServeHTTP(w, r)
}

// heldResponseWriter preserves the response bytes and Content-Length. The slow
// prefix keeps a real HTTP transfer active; the last byte waits for Release.
type heldResponseWriter struct {
	http.ResponseWriter
	ctx     context.Context
	release <-chan struct{}
}

func (w *heldResponseWriter) Write(p []byte) (int, error) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	written := 0
	for len(p) > 0 {
		select {
		case <-w.ctx.Done():
			return written, w.ctx.Err()
		case <-w.release:
			n, err := w.ResponseWriter.Write(p)
			return written + n, err
		default:
		}
		if len(p) > 1 {
			n, err := w.ResponseWriter.Write(p[:1])
			written += n
			if err != nil {
				return written, err
			}
			p = p[n:]
			if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		select {
		case <-w.ctx.Done():
			return written, w.ctx.Err()
		case <-w.release:
		case <-ticker.C:
		}
	}
	return written, nil
}
