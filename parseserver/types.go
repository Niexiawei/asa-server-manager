package parseserver

// SaveData 是存档解析对外暴露的两类列表。
// Players 直接来自 ExportAll 的 ASV_Players；
// Tribes 来自 ASV_Tribes，并已按 tribeid 注入 tamed_list / tribe_logs / player_list。
type SaveData struct {
	Players []map[string]any `json:"players"`
	Tribes  []map[string]any `json:"tribes"`
}
