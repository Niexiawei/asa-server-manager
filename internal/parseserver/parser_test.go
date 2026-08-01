package parseserver

import (
	"testing"
)

// TestBuildSaveData_Enrichment 验证部落按 tribeid 注入 player_list / tamed_list / tribe_logs。
func TestBuildSaveData_Enrichment(t *testing.T) {
	export := map[string][]map[string]any{
		keyPlayers: {
			{"playerid": 1, "name": "Alice", "tribeid": 100},
			{"playerid": 2, "name": "Bob", "tribeid": 100},
			{"playerid": 3, "name": "Carol", "tribeid": 200},
		},
		keyTribes: {
			{"tribeid": 100, "tribe": "Red", "players": 2},
			{"tribeid": 200, "tribe": "Blue", "players": 1},
			{"tribeid": 300, "tribe": "Empty", "players": 0},
		},
		keyTamed: {
			{"dinoid": 11, "creature": "Rex", "tribeid": 100},
			{"dinoid": 12, "creature": "Raptor", "tribeid": 100},
			{"dinoid": 13, "creature": "Bronto", "tribeid": 200},
		},
		keyTribeLogs: {
			{"tribeid": 100, "tribe": "Red", "logs": []string{"a", "b"}},
			{"tribeid": 200, "tribe": "Blue", "logs": []string{"c"}},
		},
	}

	data := buildSaveData(export)

	if len(data.Players) != 3 {
		t.Fatalf("expected 3 players, got %d", len(data.Players))
	}
	if len(data.Tribes) != 3 {
		t.Fatalf("expected 3 tribes, got %d", len(data.Tribes))
	}

	byID := make(map[int64]map[string]any)
	for _, tr := range data.Tribes {
		byID[toInt64(tr["tribeid"])] = tr
	}

	red := byID[100]
	if got := len(red["player_list"].([]map[string]any)); got != 2 {
		t.Errorf("tribe 100 player_list: expected 2, got %d", got)
	}
	if got := len(red["tamed_list"].([]map[string]any)); got != 2 {
		t.Errorf("tribe 100 tamed_list: expected 2, got %d", got)
	}
	if got := len(red["tribe_logs"].([]any)); got != 2 {
		t.Errorf("tribe 100 tribe_logs: expected 2, got %d", got)
	}
	// 原有字段保持不变
	if red["players"] != 2 {
		t.Errorf("tribe 100 原 players 计数应保持为 2, got %v", red["players"])
	}

	// 空部落三字段应为非 nil 空数组
	empty := byID[300]
	if got := len(empty["player_list"].([]map[string]any)); got != 0 {
		t.Errorf("tribe 300 player_list should be empty, got %d", got)
	}
	if empty["tribe_logs"] == nil {
		t.Error("tribe 300 tribe_logs should be non-nil empty slice")
	}
}

// TestBuildSaveData_Empty 空 export 不应 panic，返回空列表。
func TestBuildSaveData_Empty(t *testing.T) {
	data := buildSaveData(map[string][]map[string]any{})
	if data.Players == nil || data.Tribes == nil {
		t.Fatal("Players/Tribes 应为非 nil 空切片")
	}
	if len(data.Players) != 0 || len(data.Tribes) != 0 {
		t.Fatalf("expected empty, got players=%d tribes=%d", len(data.Players), len(data.Tribes))
	}
}

func TestToInt64(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{int(5), 5},
		{int64(7), 7},
		{float64(9), 9},
		{"123", 123},
		{nil, 0},
	}
	for _, c := range cases {
		if got := toInt64(c.in); got != c.want {
			t.Errorf("toInt64(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
