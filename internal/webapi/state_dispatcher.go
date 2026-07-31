package webapi

import (
	"asa-server/internal/realtime"
	statepkg "asa-server/internal/state"
	"context"
	"fmt"
)

// startStateChangeDispatcher 订阅 StateManager 的所有状态变更并推送到 WebSocket。
// 必须在 InitStateManager 之后调用。
func (s *APIServer) startStateChangeDispatcher(ctx context.Context) {
	subID, ch := statepkg.SubscribeStateChanges(64)
	if ch == nil {
		return
	}
	go func() {
		defer statepkg.UnsubscribeStateChanges(subID)
		for {
			select {
			case state, ok := <-ch:
				if !ok {
					return
				}
				broadcastInstanceStateChange(state)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func broadcastInstanceStateChange(state statepkg.InstanceState) {
	eventType, status, message := stateToEventFields(state)
	data := map[string]any{
		"raw_status": string(state.Status),
	}
	if state.ErrorMessage != "" {
		data["error"] = state.ErrorMessage
	}
	realtime.BroadcastServerEventWithData(eventType, state.InstanceName, message, status, data)
}

func stateToEventFields(state statepkg.InstanceState) (eventType, status, message string) {
	name := state.InstanceName
	switch state.Status {
	case statepkg.StatusStartStartInitialization:
		return "server_start_initialization", "start_initialization",
			fmt.Sprintf("Server '%s' is initializing", name)
	case statepkg.StatusStartStartInitializationSuccessful:
		return "server_start_initialization_successful", "start_initialization_successful",
			fmt.Sprintf("Server '%s' initialized, waiting to start", name)
	case statepkg.StatusStarting:
		return "server_starting", "starting", fmt.Sprintf("Server '%s' is starting", name)
	case statepkg.StatusStarted:
		return "server_started", "started", fmt.Sprintf("Server '%s' started successfully", name)
	case statepkg.StatusStopping:
		return "server_stopping", "stopping", fmt.Sprintf("Server '%s' is stopping", name)
	case statepkg.StatusStopped:
		return "server_stopped", "stopped", fmt.Sprintf("Server '%s' stopped", name)
	case statepkg.StatusStartFailed:
		msg := fmt.Sprintf("Server '%s' failed to start", name)
		if state.ErrorMessage != "" {
			msg += ": " + state.ErrorMessage
		}
		return "server_start_failed", "failed", msg
	case statepkg.StatusStopFailed:
		msg := fmt.Sprintf("Server '%s' failed to stop", name)
		if state.ErrorMessage != "" {
			msg += ": " + state.ErrorMessage
		}
		return "server_stop_failed", "failed", msg
	case statepkg.StatusRestartFailed:
		msg := fmt.Sprintf("Server '%s' failed to restart", name)
		if state.ErrorMessage != "" {
			msg += ": " + state.ErrorMessage
		}
		return "server_restart_failed", "failed", msg
	case statepkg.StatusRestarting:
		return "server_restarting", "restarting", fmt.Sprintf("Server '%s' is restarting", name)
	case statepkg.StatusRestarted:
		return "server_restarted", "restarted", fmt.Sprintf("Server '%s' restarted successfully", name)
	default:
		return "instance_state_change", string(state.Status),
			fmt.Sprintf("Server '%s' state: %s", name, state.Status)
	}
}
