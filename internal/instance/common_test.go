package instance

import (
	cfgpkg "asa-server/internal/config"
	statepkg "asa-server/internal/state"
	"asa-server/pkg/logger"
	"fmt"
	"os"
	"testing"
)

// 环境耦合：本来就依赖本机 ASA_BASEDIR 指向的数据目录，见 CLAUDE.md 的既有说明。
func Test_SaveWorldSafely(t *testing.T) {
	cfgpkg.EnsureDirectories(os.Getenv("ASA_BASEDIR"))
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
