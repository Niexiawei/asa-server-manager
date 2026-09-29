package runner

import (
	"strings"
	"testing"

	"asa-server/pkg/umuruntime"
)

// TestDescribeUnmetKeepsWording: 实例启动前两条检查的用户可见文案，插件化之后与之前
// 逐字相同（显示那条），或只改了一个衔接字（VC++ 那条补了「在」以接「但」）。
func TestDescribeUnmetKeepsWording(t *testing.T) {
	gui := Unmet{Cap: CapGUI, Readiness: umuruntime.Readiness{Definitive: true, Detail: "本机没有可用的 X 显示"}}
	got := "实例 A 启用了 ArkApi，但" + DescribeUnmet(gui) + "，已中止启动"
	if want := "实例 A 启用了 ArkApi，但本机没有可用的 X 显示；" +
		"AsaApiLoader.exe 在 Wine 下没有图形显示会静默退出，已中止启动"; got != want {
		t.Errorf("display:\n got %q\nwant %q", got, want)
	}

	vc := DescribeUnmet(Unmet{Cap: CapMSVCRT})
	for _, must := range []string{"VC++ 运行时", "asa-server setup", "linux.install_vcredist"} {
		if !strings.Contains(vc, must) {
			t.Errorf("VC++ description %q lacks %q", vc, must)
		}
	}

	if other := DescribeUnmet(Unmet{Cap: "win32.other", Readiness: umuruntime.Readiness{Detail: "x"}}); !strings.Contains(other, "win32.other") {
		t.Errorf("unknown capability description %q does not name it", other)
	}
}

// TestCheckNeedsEmptyWithoutNeeds: 纯 ArkAscendedServer.exe 什么都不需要，检查必须为空——
// 否则每个普通实例的启动都会多出告警。
func TestCheckNeedsEmptyWithoutNeeds(t *testing.T) {
	if got := CheckNeeds("", nil); len(got) != 0 {
		t.Errorf("CheckNeeds(nil) = %+v, want none", got)
	}
}
