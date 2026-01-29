package webapi

import (
	"sync"
)

// UpdateProgressWriter writes progress messages to a broadcaster
type UpdateProgressWriter struct {
	broadcaster *TaskBroadcaster
}

func (w *UpdateProgressWriter) Write(p []byte) (n int, err error) {
	w.broadcaster.SendMessage(string(p))
	return len(p), nil
}

// TaskBroadcaster manages broadcast subscriptions for a specific task
type TaskBroadcaster struct {
	msgChan     chan string
	subscribers map[chan<- string]bool
	mu          sync.RWMutex
	running     bool
	wg          sync.WaitGroup
}

// NewTaskBroadcaster creates a new task broadcaster
func NewTaskBroadcaster() *TaskBroadcaster {
	return &TaskBroadcaster{
		msgChan:     make(chan string, 100),
		subscribers: make(map[chan<- string]bool),
	}
}

// Start marks the task as running
func (tb *TaskBroadcaster) Start() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if tb.running {
		return false
	}

	// Always recreate msgChan when starting - this ensures we have a fresh channel
	// This fixes the "send on closed channel" panic when restarting a task
	if tb.msgChan != nil {
		// Close the old channel if it's not nil
		close(tb.msgChan)
	}
	tb.msgChan = make(chan string, 100)
	// Clear subscribers map and ensure it's initialized
	if tb.subscribers == nil {
		tb.subscribers = make(map[chan<- string]bool)
	} else {
		// Close all remaining subscriber channels and clear the map in one loop
		for subscriber := range tb.subscribers {
			close(subscriber)
			delete(tb.subscribers, subscriber)
		}
	}

	tb.running = true
	// Start the broadcaster goroutine
	tb.wg.Add(1)
	go tb.broadcast()
	return true
}

// SendMessage sends a message to all subscribers
func (tb *TaskBroadcaster) SendMessage(msg string) {
	tb.mu.RLock()
	running := tb.running
	tb.mu.RUnlock()

	if !running {
		return
	}

	// Use non-blocking send to avoid panics on closed channel
	select {
	case tb.msgChan <- msg:

	default:
		// Channel is full or closed, skip
	}
}

// Subscribe registers a new subscriber
// Returns a channel to receive messages and a function to unsubscribe
func (tb *TaskBroadcaster) Subscribe() (chan string, func()) {
	subscriber := make(chan string, 50)

	tb.mu.Lock()
	tb.subscribers[subscriber] = true
	tb.mu.Unlock()

	unsubscribe := func() {
		tb.mu.Lock()
		_, exists := tb.subscribers[subscriber]
		if exists {
			delete(tb.subscribers, subscriber)
			tb.mu.Unlock()
			close(subscriber)
		} else {
			tb.mu.Unlock()
		}
	}

	return subscriber, unsubscribe
}

// Stop marks the task as completed and closes all subscriber channels
func (tb *TaskBroadcaster) Stop() {
	tb.mu.Lock()

	// Only stop if we're actually running
	if !tb.running {
		tb.mu.Unlock()
		return
	}

	tb.running = false

	// Close the message channel
	if tb.msgChan != nil {
		close(tb.msgChan)
		tb.msgChan = nil
	}
	tb.mu.Unlock()

	tb.wg.Wait()

	tb.mu.Lock()
	// Close all subscriber channels and clear the map
	for subscriber := range tb.subscribers {
		delete(tb.subscribers, subscriber)
		close(subscriber)
	}
	// Clear the subscribers map to ensure it's empty for the next run
	// This isn't strictly necessary since we delete each entry, but it's safe
	tb.subscribers = make(map[chan<- string]bool)
	tb.mu.Unlock()
}

// broadcast distributes messages to all subscribers
func (tb *TaskBroadcaster) broadcast() {
	defer tb.wg.Done()
	for msg := range tb.msgChan {
		tb.mu.RLock()
		// Create a copy of subscribers to avoid issues if map changes during iteration
		subscribers := make([]chan<- string, 0, len(tb.subscribers))
		for subscriber := range tb.subscribers {
			subscribers = append(subscribers, subscriber)
		}
		tb.mu.RUnlock()

		// Send message to all subscribers with non-blocking send
		for _, subscriber := range subscribers {
			select {
			case subscriber <- msg:
			default:
				// Skip if channel is full or closed
			}
		}
	}
}

// IsRunning checks if the task is running
func (tb *TaskBroadcaster) IsRunning() bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.running
}

// InstanceStartBroadcasters manages per-instance startup broadcasters
type InstanceStartBroadcasters struct {
	mu           sync.RWMutex
	broadcasters map[string]*TaskBroadcaster
}

// NewInstanceStartBroadcasters creates a new instance start broadcasters manager
func NewInstanceStartBroadcasters() *InstanceStartBroadcasters {
	return &InstanceStartBroadcasters{
		broadcasters: make(map[string]*TaskBroadcaster),
	}
}

// Get gets or creates a broadcaster for instance startup
func (isb *InstanceStartBroadcasters) Get(instanceName string) *TaskBroadcaster {
	isb.mu.Lock()
	defer isb.mu.Unlock()

	if broadcaster, exists := isb.broadcasters[instanceName]; exists {
		return broadcaster
	}

	// Create new broadcaster for this instance
	broadcaster := NewTaskBroadcaster()
	isb.broadcasters[instanceName] = broadcaster
	return broadcaster
}

// Cleanup removes the broadcaster for an instance
func (isb *InstanceStartBroadcasters) Cleanup(instanceName string) {
	isb.mu.Lock()
	defer isb.mu.Unlock()
	if broadcaster, exists := isb.broadcasters[instanceName]; exists {
		broadcaster.Stop()
		delete(isb.broadcasters, instanceName) // Actually remove the broadcaster after stopping
	}
}
