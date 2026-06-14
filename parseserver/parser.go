package parseserver

import (
	"context"
	"fmt"
	"path/filepath"

	goarkparser "github.com/Niexiawei/go-arkparser"
	"github.com/Niexiawei/go-arkparser/common"
	"github.com/Niexiawei/go-arkparser/files"
)

type ParseType string

const (
	ParseTypePlayers ParseType = "players"
	ParseTypeTribes  ParseType = "tribes"
	ParseTypeAll     ParseType = "all"
)

// ParseSave parses an ARK save file using the go-arkparser native Go library.
func ParseSave(ctx context.Context, savePath string, parseType ParseType) (*SaveParseResult, error) {
	ws, err := files.LoadWorldSave(savePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load world save: %w", err)
	}

	mapConfig := common.GetMapConfig(filepath.Base(savePath))

	switch parseType {
	case ParseTypePlayers:
		players := goarkparser.ExportPlayers(nil, ws, mapConfig, nil)
		return &SaveParseResult{Success: true, Data: &SaveData{Players: players}}, nil

	case ParseTypeTribes:
		tribes := goarkparser.ExportTribes(ws, nil, nil)
		return &SaveParseResult{Success: true, Data: &SaveData{Tribes: tribes}}, nil

	case ParseTypeAll:
		players := goarkparser.ExportPlayers(nil, ws, mapConfig, nil)
		tribes := goarkparser.ExportTribes(ws, nil, nil)

		playerTribeMap := make(map[int64]int64)
		tribePlayerMap := make(map[int64][]int64)

		for _, p := range players {
			playerID := toInt64(p["playerid"])
			tribeID := toInt64(p["tribeid"])
			if playerID > 0 && tribeID > 0 {
				playerTribeMap[playerID] = tribeID
				tribePlayerMap[tribeID] = append(tribePlayerMap[tribeID], playerID)
			}
		}

		return &SaveParseResult{Success: true, Data: &SaveData{
			Players:        players,
			Tribes:         tribes,
			PlayerTribeMap: playerTribeMap,
			TribePlayerMap: tribePlayerMap,
		}}, nil

	default:
		return nil, fmt.Errorf("unknown parse type: %s", parseType)
	}
}

// IsParserAvailable returns true since the Go parser is always available.
func IsParserAvailable() bool {
	return true
}

// toInt64 converts an interface{} value to int64.
func toInt64(v any) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int32:
		return int64(val)
	case int64:
		return val
	case float64:
		return int64(val)
	default:
		return 0
	}
}
