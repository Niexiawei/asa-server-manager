package asaserver

import (
	"asa-server/logger"
	"log"
	"testing"
)

func init() {
	if err := EnsureDirectories(); err != nil {
		log.Fatal(err)
	}

	logger.InitLoggerWithBaseDir(BaseDir)
	// Initialize log mapping from persistent storage
	if err := InitializeLogMapping(); err != nil {
		log.Fatal(err)
	}
}

func Test_SetMessageOfTheDay(t *testing.T) {
	err := SetMessageOfTheDay("ces99", "哈哈哈哈123456", 30)
	if err != nil {
		t.Error(err)
	}
}
