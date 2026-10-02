package config

import (
	"asa-server/pkg/logger"
	"log"
	"os"
	"path/filepath"
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

// 环境耦合：改的是本机真实存在的实例 ces99 的 GameUserSettings.ini。
// SetMessageOfTheDay 只改文件、不要求实例在运行，所以前提是「这个实例存在」，
// 没有它的机器（CI、WSL、别人的开发机）上跳过，而不是报失败。
func Test_SetMessageOfTheDay(t *testing.T) {
	const instance = "ces99"
	gus := filepath.Join(InstancesDir, instance, "Config", "GameUserSettings.ini")
	if _, err := os.Stat(gus); err != nil {
		t.Skipf("本机没有实例 %s 的 GameUserSettings.ini（%v），跳过", instance, err)
	}

	err := SetMessageOfTheDay(instance, "哈哈哈哈123456", 30)
	if err != nil {
		t.Error(err)
	}
}
