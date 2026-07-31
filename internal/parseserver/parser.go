package parseserver

import (
	cfgpkg "asa-server/config"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/Niexiawei/go-arkparser/arkmonitor"
	"github.com/Niexiawei/go-arkparser/files"
)

// ExportAll 输出的分类键。
const (
	keyPlayers   = "ASV_Players"
	keyTribes    = "ASV_Tribes"
	keyTamed     = "ASV_Tamed"
	keyTribeLogs = "ASV_TribeLogs"
)

// ParseInstanceSave 按实例名解析存档，返回玩家列表与富化后的部落列表。
// 内部自动定位实例的 .ark（其同目录的 .arkprofile / .arktribe 由解析管线自动发现），
// 三类文件齐备才能得到完整的玩家/部落信息。
func ParseInstanceSave(_ context.Context, instanceName string) (*SaveData, error) {
	arkPath, err := locateInstanceArkPath(instanceName)
	if err != nil {
		return nil, err
	}

	export, err := arkmonitor.ExportSave(arkPath, true, files.WithMmap())
	if err != nil {
		return nil, fmt.Errorf("解析存档失败 %s: %w", instanceName, err)
	}

	return buildSaveData(export), nil
}

// buildSaveData 从 ExportAll 富数据投影出对外的两类列表：
// Players 原样透传；Tribes 按 tribeid 注入 tamed_list / tribe_logs / player_list。
func buildSaveData(export map[string][]map[string]any) *SaveData {
	players := export[keyPlayers]
	if players == nil {
		players = []map[string]any{}
	}

	return &SaveData{
		Players: players,
		Tribes:  enrichTribes(export[keyTribes], players, export[keyTamed], export[keyTribeLogs]),
	}
}

// enrichTribes 为每条部落记录注入 tamed_list（已驯服生物）、tribe_logs（部落日志）、
// player_list（该部落下玩家）。均以 tribeid 关联，无对应数据时给空数组。
func enrichTribes(tribes, players, tamed, tribeLogs []map[string]any) []map[string]any {
	playersByTribe := groupByTribe(players)
	tamedByTribe := groupByTribe(tamed)

	logsByTribe := make(map[int64][]any, len(tribeLogs))
	for _, rec := range tribeLogs {
		tid := toInt64(rec["tribeid"])
		if logs, ok := rec["logs"].([]string); ok {
			arr := make([]any, len(logs))
			for i, s := range logs {
				arr[i] = s
			}
			logsByTribe[tid] = arr
		} else if logs, ok := rec["logs"].([]any); ok {
			logsByTribe[tid] = logs
		}
	}

	result := make([]map[string]any, 0, len(tribes))
	for _, tribe := range tribes {
		tid := toInt64(tribe["tribeid"])

		enriched := make(map[string]any, len(tribe)+3)
		maps.Copy(enriched, tribe)
		enriched["player_list"] = ensureList(playersByTribe[tid])
		enriched["tamed_list"] = ensureList(tamedByTribe[tid])
		enriched["tribe_logs"] = ensureAnyList(logsByTribe[tid])

		result = append(result, enriched)
	}
	return result
}

// groupByTribe 按记录的 tribeid 分组。
func groupByTribe(records []map[string]any) map[int64][]map[string]any {
	grouped := make(map[int64][]map[string]any)
	for _, rec := range records {
		tid := toInt64(rec["tribeid"])
		grouped[tid] = append(grouped[tid], rec)
	}
	return grouped
}

func ensureList(v []map[string]any) []map[string]any {
	if v == nil {
		return []map[string]any{}
	}
	return v
}

func ensureAnyList(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}

// locateInstanceArkPath 由实例名解析出世界存档 .ark 的绝对路径：
// {InstancesDir}/{instance}/Save/{MapName}/{MapName}.ark。
// 该发现逻辑归属解析层，不放在 API 层。
func locateInstanceArkPath(instanceName string) (string, error) {
	config, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil {
		return "", fmt.Errorf("加载实例配置失败 %s: %w", instanceName, err)
	}

	arkPath := filepath.Join(cfgpkg.InstancesDir, instanceName, "Save", config.MapName, config.MapName+".ark")
	if _, err := os.Stat(arkPath); err != nil {
		return "", fmt.Errorf("实例 %s 的存档文件不存在: %s", instanceName, arkPath)
	}
	return arkPath, nil
}

// toInt64 尽力把任意值转 int64（tribeid/playerid 在不同记录里可能是 int/float64/string）。
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
	case float32:
		return int64(val)
	case string:
		var n int64
		_, _ = fmt.Sscanf(val, "%d", &n)
		return n
	default:
		return 0
	}
}
