package config

import (
	"asa-server/internal/appconfig"
	"asa-server/pkg/logger"
	"os"
	"path/filepath"
	"testing"
)

// 环境耦合：Test_SetMessageOfTheDay 依赖本机真实的数据目录。数据目录与生产一样从
// config.yaml 解析（ASA_BASEDIR 已移除，见 docs/APPCONFIG_BASEDIR_PLAN.md Part 2）：
// go test 下「exe 同级」是测试二进制的临时目录，所以只有 ASA_CFG 指向一份写了
// basedir 的配置时才会找到真实数据目录，其余机器上那条用例跳过。
//
// 只设目录变量、不建目录（SetDirectories 而不是 EnsureDirectories）：没有配置时
// BaseDir 落在测试二进制旁边，建出来也没人用；以前这里会在源码目录下留下
// instances/、server-files/ 等空目录（docs/TEST_ENV_COUPLING_PLAN.md T2）。
func init() {
	baseDir, _ := appconfig.Load()
	SetDirectories(baseDir)

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
