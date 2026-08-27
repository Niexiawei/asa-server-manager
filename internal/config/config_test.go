package config

import (
	"asa-server/pkg/logger"
	"log"
	"os"
	"testing"
)

// 环境耦合：这个测试文件本来就依赖本机 ASA_BASEDIR 指向的数据目录，见 CLAUDE.md
// 的既有说明。EnsureDirectories 不再自行解析 BaseDir（见
// docs/APPCONFIG_BASEDIR_PLAN.md），这里只是把测试原先依赖的同一个环境变量显式传
// 进去，签名层面适配，不改变这个测试的环境耦合性质。
func init() {
	if err := EnsureDirectories(os.Getenv("ASA_BASEDIR")); err != nil {
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
