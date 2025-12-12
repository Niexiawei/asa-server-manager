package webapi

// UpdateProgressWriter writes update progress to broadcast channel
type UpdateProgressWriter struct {
	s *APIServer
}

func (w *UpdateProgressWriter) Write(p []byte) (n int, err error) {
	select {
	case w.s.updateProgressChan <- string(p):
		return len(p), nil
	default:
		return len(p), nil
	}
}

// broadcastUpdateProgress broadcasts update progress to all subscribers
func (s *APIServer) broadcastUpdateProgress() {
	for msg := range s.updateProgressChan {
		s.updateSubMu.RLock()
		for subscriber := range s.updateSubscribers {
			select {
			case subscriber <- msg:
			default:
				// Skip if channel is full
			}
		}
		s.updateSubMu.RUnlock()
	}
}

// subscribeUpdateProgress registers a new subscriber to update progress
// Returns a channel to receive messages and a function to unsubscribe
func (s *APIServer) subscribeUpdateProgress() (chan string, func()) {
	subscriber := make(chan string, 50)

	// Register subscriber
	s.updateSubMu.Lock()
	s.updateSubscribers[subscriber] = true
	s.updateSubMu.Unlock()

	// Return channel and unsubscribe function
	unsubscribe := func() {
		s.updateSubMu.Lock()
		_, exists := s.updateSubscribers[subscriber]
		if exists {
			delete(s.updateSubscribers, subscriber)
			s.updateSubMu.Unlock()
			// Only close if we're the ones who removed it from map
			close(subscriber)
		} else {
			s.updateSubMu.Unlock()
		}
	}

	return subscriber, unsubscribe
}

// startUpdateTask starts the server update task if not already running
// Returns true if task was started, false if already running
func (s *APIServer) startUpdateTask() bool {
	s.updateSubMu.Lock()
	defer s.updateSubMu.Unlock()

	if s.updating {
		return false
	}

	s.updating = true
	return true
}

// stopUpdateTask marks the update task as completed and closes all subscriber channels
func (s *APIServer) stopUpdateTask() {
	s.updateSubMu.Lock()
	defer s.updateSubMu.Unlock()
	s.updating = false
	// Close all subscriber channels to signal SSE clients that task is complete
	for subscriber := range s.updateSubscribers {
		// Safely close the channel (subscriber is a receive-only channel in the map)
		// We can safely close it here since we own the map
		delete(s.updateSubscribers, subscriber)
		close(subscriber)
	}
}

// isUpdating checks if an update task is currently running
func (s *APIServer) isUpdating() bool {
	s.updateSubMu.RLock()
	defer s.updateSubMu.RUnlock()
	return s.updating
}
