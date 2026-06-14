package parseserver

import (
	"context"
	"testing"
)

const testSavePath = `E:\asa_server_data\server-files\ShooterGame\Saved\meijue\Extinction_WP\Extinction_WP.ark`

func TestParseSave_Players(t *testing.T) {
	result, err := ParseSave(context.Background(), testSavePath, ParseTypePlayers)
	if err != nil {
		t.Fatalf("ParseSave failed: %v", err)
	}

	if !result.Success {
		t.Fatalf("Parse returned success=false: %s", result.Error)
	}

	t.Logf("Players found: %d", len(result.Data.Players))
	for _, p := range result.Data.Players {
		t.Logf("  Player: id=%v name=%v tribe=%v", p["playerid"], p["name"], p["tribeid"])
	}
}

func TestParseSave_Tribes(t *testing.T) {
	result, err := ParseSave(context.Background(), testSavePath, ParseTypeTribes)
	if err != nil {
		t.Fatalf("ParseSave failed: %v", err)
	}

	if !result.Success {
		t.Fatalf("Parse returned success=false: %s", result.Error)
	}

	t.Logf("Tribes found: %d", len(result.Data.Tribes))
	for _, tri := range result.Data.Tribes {
		t.Logf("  Tribe: id=%v name=%v players=%v", tri["tribeid"], tri["tribe"], tri["players"])
	}
}

func TestParseSave_All(t *testing.T) {
	result, err := ParseSave(context.Background(), testSavePath, ParseTypeAll)
	if err != nil {
		t.Fatalf("ParseSave failed: %v", err)
	}

	if !result.Success {
		t.Fatalf("Parse returned success=false: %s", result.Error)
	}

	t.Logf("Players: %d, Tribes: %d", len(result.Data.Players), len(result.Data.Tribes))
	t.Logf("Player-Tribe map: %v", result.Data.PlayerTribeMap)
	t.Logf("Tribe-Player map: %v", result.Data.TribePlayerMap)
}

func TestParseSave_FileNotFound(t *testing.T) {
	_, err := ParseSave(context.Background(), "/nonexistent/file.ark", ParseTypePlayers)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
	t.Logf("Expected error: %v", err)
}

func TestIsParserAvailable(t *testing.T) {
	if !IsParserAvailable() {
		t.Fatal("IsParserAvailable returned false")
	}
}
