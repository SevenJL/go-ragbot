package config

import (
	"log"
	"os"
	"sync"
	"time"
)

// Watcher polls a config file for changes and calls the callback when the
// modification time changes. Suitable for lightweight hot-reload without
// external dependencies (no fsnotify needed).
type Watcher struct {
	path     string
	interval time.Duration
	onChange func(*Config)
	lastMod  time.Time

	mu     sync.Mutex
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewWatcher creates a config file watcher. interval is the polling period;
// 5 seconds is a reasonable default. onChange is called with the reloaded
// config on every detected modification.
func NewWatcher(path string, interval time.Duration, onChange func(*Config)) *Watcher {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Watcher{
		path:     path,
		interval: interval,
		onChange: onChange,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start begins polling. Only one watcher should be started per process.
func (w *Watcher) Start() {
	if fi, err := os.Stat(w.path); err == nil {
		w.lastMod = fi.ModTime()
	}
	go w.loop()
}

// Stop gracefully terminates the polling loop.
func (w *Watcher) Stop() {
	close(w.stopCh)
	<-w.doneCh
}

func (w *Watcher) loop() {
	defer close(w.doneCh)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			fi, err := os.Stat(w.path)
			if err != nil {
				continue
			}
			mod := fi.ModTime()
			w.mu.Lock()
			last := w.lastMod
			w.mu.Unlock()

			if mod.After(last) {
				log.Printf("config: %s changed, reloading...", w.path)
				cfg, err := Load(w.path)
				if err != nil {
					log.Printf("config: reload failed: %v (keeping old config)", err)
					continue
				}
				w.mu.Lock()
				w.lastMod = mod
				w.mu.Unlock()
				w.onChange(cfg)
			}
		}
	}
}
