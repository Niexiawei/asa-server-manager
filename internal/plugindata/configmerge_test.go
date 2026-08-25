package plugindata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 键顺序必须保持原样。这条不能用 map[string]any 实现 —— Go 的 map 无序，
// encoding/json 会按键名排序，合并一次就把用户手改过的配置文件彻底重排。
func TestMergePreservesKeyOrder(t *testing.T) {
	instance := []byte(`{"Zulu":1,"Alpha":2,"Mike":3}`)
	mirror := []byte(`{"Alpha":99,"Zulu":99,"Mike":99}`)

	merged, err := MergeConfigJSON(instance, mirror)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if got := keyOrder(t, merged); strings.Join(got, ",") != "Zulu,Alpha,Mike" {
		t.Errorf("键顺序被改动: %v，期望 Zulu,Alpha,Mike", got)
	}
}

// 两侧都有的键恒取实例侧 —— 用户在管理器里配的东西是权威，
// 插件运行期算出来的值不保留。这是已定的取舍，没有例外名单。
func TestMergeInstanceValueAlwaysWins(t *testing.T) {
	instance := []byte(`{"ClusterSyncTime":300,"UseMysql":false}`)
	mirror := []byte(`{"ClusterSyncTime":60,"UseMysql":true}`)

	merged, err := MergeConfigJSON(instance, mirror)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(merged, &obj); err != nil {
		t.Fatalf("结果不是合法 JSON: %v\n%s", err, merged)
	}
	if v, _ := obj["ClusterSyncTime"].(float64); v != 300 {
		t.Errorf("ClusterSyncTime = %v，期望保留实例侧的 300", obj["ClusterSyncTime"])
	}
	if obj["UseMysql"] != false {
		t.Errorf("UseMysql = %v，期望保留实例侧的 false", obj["UseMysql"])
	}
}

// 插件更新会往 config.json 里写入新增项（已验证），这些项必须并进来，
// 且追加在末尾而不是插到中间。
func TestMergeAddsNewKeysAtEnd(t *testing.T) {
	instance := []byte(`{"A":1,"B":2}`)
	mirror := []byte(`{"A":1,"NewFeature":true,"B":2}`)

	merged, err := MergeConfigJSON(instance, mirror)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if got := keyOrder(t, merged); strings.Join(got, ",") != "A,B,NewFeature" {
		t.Errorf("新增键应追加末尾，实际顺序 %v", got)
	}
}

// 合并必须递归进嵌套对象：CrosschatAscended 的配置有近 8 KB，
// 插件新增的项很可能落在某个嵌套对象里，只做顶层合并会漏掉。
func TestMergeRecursesIntoNestedObjects(t *testing.T) {
	instance := []byte(`{"Discord":{"Token":"mine","Channel":"general"}}`)
	mirror := []byte(`{"Discord":{"Token":"default","Channel":"general","NewOption":42}}`)

	merged, err := MergeConfigJSON(instance, mirror)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	var obj struct {
		Discord struct {
			Token     string `json:"Token"`
			NewOption *int   `json:"NewOption"`
		} `json:"Discord"`
	}
	if err := json.Unmarshal(merged, &obj); err != nil {
		t.Fatalf("结果不是合法 JSON: %v\n%s", err, merged)
	}
	if obj.Discord.Token != "mine" {
		t.Errorf("嵌套对象里两侧都有的键仍应取实例侧，实际 %q", obj.Discord.Token)
	}
	if obj.Discord.NewOption == nil || *obj.Discord.NewOption != 42 {
		t.Errorf("嵌套对象里插件新增的键应被并入，实际 %v", obj.Discord.NewOption)
	}
}

// 数组整体当作一个值，不做逐元素合并 ——
// 逐元素合并需要知道元素的身份键，而那是每个插件各自的约定。
func TestMergeTreatsArraysAsWholeValues(t *testing.T) {
	instance := []byte(`{"Admins":["me"]}`)
	mirror := []byte(`{"Admins":["default1","default2"]}`)

	merged, err := MergeConfigJSON(instance, mirror)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	var obj struct {
		Admins []string `json:"Admins"`
	}
	if err := json.Unmarshal(merged, &obj); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	if len(obj.Admins) != 1 || obj.Admins[0] != "me" {
		t.Errorf("数组应整体取实例侧，实际 %v", obj.Admins)
	}
}

// 大整数与浮点必须原样保留：走 map[string]any 会把它们全变成 float64，
// 大 ID 会被改写成科学计数法或丢精度。
func TestMergePreservesNumberLiterals(t *testing.T) {
	instance := []byte(`{"SteamID":76561198000000000,"Ratio":1.50}`)
	mirror := []byte(`{"SteamID":0,"Ratio":0,"New":1}`)

	merged, err := MergeConfigJSON(instance, mirror)
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	s := string(merged)
	if !strings.Contains(s, "76561198000000000") {
		t.Errorf("大整数被改写了:\n%s", s)
	}
	if !strings.Contains(s, "1.50") {
		t.Errorf("浮点字面量被改写了:\n%s", s)
	}
}

// 一侧不是合法 JSON 对象时宁可什么都不做：
// 实例侧那份是用户改过的，覆盖掉就找不回来了。
func TestMergeConfigIntoKeepsInstanceFileOnParseError(t *testing.T) {
	mirrorPlugin := t.TempDir()
	instPlugin := t.TempDir()

	original := `{"Kept":true}`
	writeFile(t, filepath.Join(instPlugin, configFileName), original)
	writeFile(t, filepath.Join(mirrorPlugin, configFileName), `this is not json`)

	mergeConfigInto(mirrorPlugin, instPlugin, configFileName, true)

	if got := readFile(t, filepath.Join(instPlugin, configFileName)); got != original {
		t.Errorf("镜像侧解析失败时应原样保留实例侧配置，实际 %q", got)
	}
}

// 合并前把镜像侧原文另存一份，出问题能回溯。
func TestMergeConfigIntoWritesBackup(t *testing.T) {
	mirrorPlugin := t.TempDir()
	instPlugin := t.TempDir()

	writeFile(t, filepath.Join(instPlugin, configFileName), `{"A":1}`)
	writeFile(t, filepath.Join(mirrorPlugin, configFileName), `{"A":2,"B":3}`)

	mergeConfigInto(mirrorPlugin, instPlugin, configFileName, true)

	bak := filepath.Join(instPlugin, configBackupName)
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("应留下镜像侧原文备份: %v", err)
	}
	if got := readFile(t, bak); got != `{"A":2,"B":3}` {
		t.Errorf("备份内容应是镜像侧原文，实际 %q", got)
	}
}

// keyOrder 按出现顺序取出顶层键名。
func keyOrder(t *testing.T, data []byte) []string {
	t.Helper()
	obj, err := parseOrderedObject(data)
	if err != nil {
		t.Fatalf("解析合并结果失败: %v\n%s", err, data)
	}
	out := make([]string, 0, len(obj))
	for _, m := range obj {
		out = append(out, m.Key)
	}
	return out
}
