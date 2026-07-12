package webapi

import (
	"asa-server/asaserver"
	"asa-server/httpserver"
	"context"
	"fmt"
)

// startStateChangeDispatcher 订阅 StateManager 的所有状态变更并推送到 WebSocket。
// 必须在 InitStateManager 之后调用。
func (s *APIServer) startStateChangeDispatcher(ctx context.Context) {
	subID, ch := asaserver.SubscribeStateChanges(64)
	if ch == nil {
		return
	}
	go func() {
		defer asaserver.UnsubscribeStateChanges(subID)
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

func broadcastInstanceStateChange(state asaserver.InstanceState) {
	eventType, status, message := stateToEventFields(state)
	data := map[string]any{
		"raw_status": string(state.Status),
	}
	if state.ErrorMessage != "" {
		data["error"] = state.ErrorMessage
	}
	httpserver.BroadcastServerEventWithData(eventType, state.InstanceName, message, status, data)
}

func stateToEventFields(state asaserver.InstanceState) (eventType, status, message string) {
	name := state.InstanceName
	switch state.Status {
	case asaserver.StatusStartStartInitialization:
		return "server_start_initialization", "start_initialization",
			fmt.Sprintf("Server '%s' is initializing", name)
	case asaserver.StatusStartStartInitializationSuccessful:
		return "server_start_initialization_successful", "start_initialization_successful",
			fmt.Sprintf("Server '%s' initialized, waiting to start", name)
	case asaserver.StatusStarting:
		return "server_starting", "starting", fmt.Sprintf("Server '%s' is starting", name)
	case asaserver.StatusStarted:
		return "server_started", "started", fmt.Sprintf("Server '%s' started successfully", name)
	case asaserver.StatusStopping:
		return "server_stopping", "stopping", fmt.Sprintf("Server '%s' is stopping", name)
	case asaserver.StatusStopped:
		return "server_stopped", "stopped", fmt.Sprintf("Server '%s' stopped", name)
	case asaserver.StatusStartFailed:
		msg := fmt.Sprintf("Server '%s' failed to start", name)
		if state.ErrorMessage != "" {
			msg += ": " + state.ErrorMessage
		}
		return "server_start_failed", "failed", msg
	case asaserver.StatusStopFailed:
		msg := fmt.Sprintf("Server '%s' failed to stop", name)
		if state.ErrorMessage != "" {
			msg += ": " + state.ErrorMessage
		}
		return "server_stop_failed", "failed", msg
	case asaserver.StatusRestartFailed:
		msg := fmt.Sprintf("Server '%s' failed to restart", name)
		if state.ErrorMessage != "" {
			msg += ": " + state.ErrorMessage
		}
		return "server_restart_failed", "failed", msg
	case asaserver.StatusRestarting:
		return "server_restarting", "restarting", fmt.Sprintf("Server '%s' is restarting", name)
	case asaserver.StatusRestarted:
		return "server_restarted", "restarted", fmt.Sprintf("Server '%s' restarted successfully", name)
	default:
		return "instance_state_change", string(state.Status),
			fmt.Sprintf("Server '%s' state: %s", name, state.Status)
	}
}
