package config

import (
	"asa-server/pkg/logger"
	"log"
	"testing"
)

func init() {
	if err := EnsureDirectories(); err != nil {
		log.Fatal(err)
	}

	logger.InitLoggerWithBaseDir(BaseDir)
}

func Test_SetMessageOfTheDay(t *testing.T) {
	err := SetMessageOfTheDay("ces99", "哈哈哈哈123456", 30)
	if err != nil {
		t.Error(err)
	}
}
