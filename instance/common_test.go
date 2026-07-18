package instance

import (
	cfgpkg "asa-server/config"
	"asa-server/logger"
	statepkg "asa-server/state"
	"fmt"
	"testing"
)

func Test_SaveWorldSafely(t *testing.T) {
	cfgpkg.EnsureDirectories()
	logger.InitLoggerWithBaseDir(cfgpkg.BaseDir)
	fmt.Println(cfgpkg.BaseDir)
	err := SaveWorldSafely("ces99")
	if err != nil {
		t.Error(err)
		return
	}
}

func Test_GetAllInstanceNames(t *testing.T) {
	err := statepkg.InitStateManager("D:\\golang\\asa-server")
	if err != nil {
		t.Error(err)
		return
	}
	if res, err := statepkg.GetAllInstanceNames(); err != nil {
		t.Error(err)
		return
	} else {
		fmt.Println(res)
	}

	res2, err := statepkg.GetInstanceStateHistory("jibian", 500)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(res2)

	res23, err := statepkg.GetInstanceStateHistory("meijue", 500)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(res23)

	res3, err := statepkg.GetLatestInstanceState("meijue")
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Println(res3)
}
