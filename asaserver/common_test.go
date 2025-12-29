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

func Test_GetAllInstanceNames(t *testing.T) {
	err := InitStateManager("D:\\golang\\asa-server")
	if err != nil {
		t.Error(err)
		return
	}
	if res, err := GetAllInstanceNames(); err != nil {
		t.Error(err)
		return
	} else {
		fmt.Println(res)
	}

	res2, err := GetInstanceStateHistory("jibian", 500)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(res2)

	res23, err := GetInstanceStateHistory("meijue", 500)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(res23)

	res3, err := GetLatestInstanceState("meijue")
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(res3)
}
