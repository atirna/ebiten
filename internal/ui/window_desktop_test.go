// Copyright 2026 The Ebitengine Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !android && !ios && !js && !nintendosdk && !playstation5

package ui

import (
	"sync"
	"testing"
)

// recordingWindow is a backendWindow whose applyX methods read the recorded
// setting, as glfwWindow's do, and keep what they saw.
type recordingWindow struct {
	nullWindow

	ui *UserInterface

	mu           sync.Mutex
	applied      map[string]bool
	onSetSize    func(int, int)
	onSetMonitor func(*Monitor)
}

func (w *recordingWindow) record(name string, read func() bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applied[name] = read()
}

func (w *recordingWindow) get(name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.applied[name]
}

func (w *recordingWindow) applyDecorated() {
	w.record("decorated", w.ui.desktopWindow.isInitWindowDecorated)
}

func (w *recordingWindow) applyFloating() {
	w.record("floating", w.ui.desktopWindow.isInitWindowFloating)
}

func (w *recordingWindow) applyMousePassthrough() {
	w.record("mouse passthrough", w.ui.desktopWindow.isInitWindowMousePassthrough)
}

func (w *recordingWindow) SetSize(width, height int) {
	w.onSetSize(width, height)
}

func (w *recordingWindow) SetMonitor(monitor *Monitor) {
	w.onSetMonitor(monitor)
}

// applyAll stands in for the block createWindow runs once the backend is published.
func (w *recordingWindow) applyAll() {
	w.applyDecorated()
	w.applyFloating()
	w.applyMousePassthrough()
}

type recordingBackend struct {
	uiBackend
	window *recordingWindow
}

func (b *recordingBackend) Window() backendWindow {
	return b.window
}

// These settings reach the window only as pre-creation hints, so nothing
// re-applies them later on its own.
var windowSetters = []struct {
	name  string
	set   func(*desktopWindow, bool)
	store func(*desktopWindow) bool
}{
	{"decorated", (*desktopWindow).SetDecorated, (*desktopWindow).isInitWindowDecorated},
	{"floating", (*desktopWindow).SetFloating, (*desktopWindow).isInitWindowFloating},
	{"mouse passthrough", (*desktopWindow).SetMousePassthrough, (*desktopWindow).isInitWindowMousePassthrough},
}

func newRecordingUI(t *testing.T) (*UserInterface, *recordingWindow) {
	t.Helper()
	u := &UserInterface{}
	if err := u.init(); err != nil {
		t.Fatal(err)
	}
	return u, &recordingWindow{ui: u, applied: map[string]bool{}}
}

// A setter must record its value whether or not a backend is published: the
// startup reads the record after publishing, and a stale record would clobber
// the window (#3481, #3633).
func TestWindowSetterRecordsValueForStartupToRead(t *testing.T) {
	for _, setter := range windowSetters {
		t.Run(setter.name, func(t *testing.T) {
			// Setting what is already recorded would prove nothing: decorated
			// starts out true, the other two false.
			u, window := newRecordingUI(t)
			want := !setter.store(&u.desktopWindow)

			// Set during the startup, before the backend is published.
			setter.set(&u.desktopWindow, want)
			u.setRunningBackend(&recordingBackend{window: window})
			window.applyAll()
			if got := window.get(setter.name); got != want {
				t.Errorf("set before the backend was published: window has %t, want %t", got, want)
			}

			// Set once the backend is published: the record must follow, or the
			// next apply reverts the window to it.
			u2, window2 := newRecordingUI(t)
			u2.setRunningBackend(&recordingBackend{window: window2})
			setter.set(&u2.desktopWindow, want)
			if got, applied := setter.store(&u2.desktopWindow), window2.get(setter.name); got != want || applied != want {
				t.Errorf("set after the backend was published: record has %t and the window has %t, want %t", got, applied, want)
			}
		})
	}
}

// The reported repro is a setter racing RunGame, so run one against the startup
// order under -race: however they interleave, the value must reach the window.
func TestWindowSetterRacingStartupIsNotLost(t *testing.T) {
	for _, setter := range windowSetters {
		t.Run(setter.name, func(t *testing.T) {
			for range 200 {
				u, window := newRecordingUI(t)
				want := !setter.store(&u.desktopWindow)

				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					setter.set(&u.desktopWindow, want)
				}()

				// What initOnMainThread and createWindow do: read the setting as
				// a hint, create the window, publish the backend, read again.
				hint := setter.store(&u.desktopWindow)
				u.setRunningBackend(&recordingBackend{window: window})
				window.applyAll()

				wg.Wait()

				if got := window.get(setter.name); got != want {
					t.Fatalf("window has %t, want %t: value lost (hint read as %t)", got, want, hint)
				}
			}
		})
	}
}

func TestWindowSizeAndMonitorRecordedBeforeBackendCall(t *testing.T) {
	u, window := newRecordingUI(t)
	u.setRunningBackend(&recordingBackend{window: window})
	called := 0
	window.onSetSize = func(width, height int) {
		called++
		if width != 800 || height != 600 {
			t.Errorf("backend size = %dx%d, want 800x600", width, height)
		}
		if w, h := u.desktopWindow.getInitWindowSizeInDIP(); w != width || h != height {
			t.Errorf("recorded size = %dx%d, backend received %dx%d", w, h, width, height)
		}
	}
	monitor := &Monitor{}
	window.onSetMonitor = func(got *Monitor) {
		called++
		if got != monitor || u.getInitMonitor() != monitor {
			t.Error("monitor must be recorded before calling the backend with it")
		}
	}
	u.desktopWindow.SetSize(800, 600)
	u.desktopWindow.SetMonitor(monitor)
	if called != 2 {
		t.Errorf("backend calls = %d, want 2", called)
	}
}
