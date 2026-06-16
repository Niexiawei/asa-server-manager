package parseserver

import (
	"time"

	"github.com/Niexiawei/go-arkparser/arkmonitor"
)

// SaveParseResult represents the result of parsing a save file (used by on-demand HTTP API)
type SaveParseResult struct {
	Success bool      `json:"success"`
	Data    *SaveData `json:"data,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// SaveData contains the parsed save data from go-arkparser (used by on-demand HTTP API)
type SaveData struct {
	Players        []map[string]any  `json:"players,omitempty"`
	Tribes         []map[string]any  `json:"tribes,omitempty"`
	PlayerTribeMap map[int64]int64   `json:"player_tribe_map,omitempty"`
	TribePlayerMap map[int64][]int64 `json:"tribe_player_map,omitempty"`
}

// CachedSaveData contains the monitored snapshot data (used by cache and SSE broadcast)
type CachedSaveData struct {
	Players   map[int]*arkmonitor.PlayerSnapshot   `json:"players,omitempty"`
	Tribes    map[int]*arkmonitor.TribeSnapshot     `json:"tribes,omitempty"`
	Timestamp time.Time                              `json:"timestamp"`
}
