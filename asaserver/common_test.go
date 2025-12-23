package asaserver

import (
	"asa-server/logger"
	"fmt"
	"testing"
)

func Test_SaveWorldSafely(t *testing.T) {
	EnsureDirectories()
	logger.InitLoggerWithBaseDir(BaseDir)
	fmt.Println(BaseDir)
	err := SaveWorldSafely("ces99")
	if err != nil {
		t.Error(err)
		return
	}
}
