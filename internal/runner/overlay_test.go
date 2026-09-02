package runner

import (
	"path/filepath"
	"testing"
)

// reconcileOverlays 决定「哪些挂载是我们的」全靠这个映射。认漏了 = 崩溃残留
// 永远清不掉；认多了 = 有可能去卸别人的挂载，所以两个方向都要钉。
func TestOverlayKeyFromMerged(t *testing.T) {
	root := filepath.Join("/opt", "asa", "umu-prefix-overlay")

	cases := []struct {
		name  string
		mount string
		want  string
		ok    bool
	}{
		{"正常的可写层", filepath.Join(root, "jibian-pve", "merged"), "jibian-pve", true},
		{"upper 不是挂载点", filepath.Join(root, "jibian-pve", "upper"), "", false},
		{"实例目录本身不是挂载点", filepath.Join(root, "jibian-pve"), "", false},
		{"根目录本身", root, "", false},
		{"多一层目录", filepath.Join(root, "a", "b", "merged"), "", false},
		{"别人的挂载", filepath.Join("/var", "lib", "docker", "overlay2", "x", "merged"), "", false},
		{"名字撞词干的兄弟目录", filepath.Join("/opt", "asa", "umu-prefix-overlay2", "x", "merged"), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := overlayKeyFromMerged(root, c.mount)
			if ok != c.ok || got != c.want {
				t.Errorf("overlayKeyFromMerged(%q) = (%q, %v), want (%q, %v)", c.mount, got, ok, c.want, c.ok)
			}
		})
	}
}

// mountinfo 的可选字段个数不固定，所以只能按 " - " 分隔符切；按固定列号切会在
// 带 shared/master 标记的机器上整体错位。
func TestParseOverlayMounts(t *testing.T) {
	const sample = `21 26 0:19 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw
26 1 8:2 / / rw,relatime - ext4 /dev/sda2 rw
120 26 0:52 / /opt/asa/umu-prefix-overlay/a/merged rw,relatime - overlay overlay rw,lowerdir=/opt/asa/umu-prefix,upperdir=/opt/asa/umu-prefix-overlay/a/upper,workdir=/opt/asa/umu-prefix-overlay/a/work
131 26 0:53 / /var/lib/docker/overlay2/deadbeef/merged rw,relatime shared:99 master:2 - overlay overlay rw,lowerdir=/x,upperdir=/y,workdir=/z
140 26 0:54 / /mnt/has\040space rw,relatime - overlay overlay rw
150 26 0:55 / /mnt/tmpfs rw - tmpfs tmpfs rw`

	got := parseOverlayMounts(sample)

	want := []string{
		"/opt/asa/umu-prefix-overlay/a/merged",
		"/var/lib/docker/overlay2/deadbeef/merged",
		"/mnt/has space",
	}
	if len(got) != len(want) {
		t.Fatalf("解析出 %d 个 overlay 挂载，期望 %d：%v", len(got), len(want), got)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("缺少挂载点 %q（解析结果 %v）", w, got)
		}
	}
	// 非 overlay 的行不能混进来，否则 reconcile 会去卸载 tmpfs。
	if got["/mnt/tmpfs"] || got["/"] {
		t.Errorf("把非 overlay 的挂载也算进来了：%v", got)
	}
}

func TestUnescapeMountPath(t *testing.T) {
	cases := map[string]string{
		"/plain/path":       "/plain/path",
		`/has\040space`:     "/has space",
		`/tab\011here`:      "/tab\there",
		`/back\134slash`:    `/back\slash`,
		`/not\09escaped`:    `/not\09escaped`, // 不是合法八进制三位，原样保留
		`/trailing\04`:      `/trailing\04`,
		`/a\040b\040c`:      "/a b c",
		"/no/escape/at/all": "/no/escape/at/all",
	}
	for in, want := range cases {
		if got := unescapeMountPath(in); got != want {
			t.Errorf("unescapeMountPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// 挂载参数是逗号分隔、冒号又是 lowerdir 的层间分隔符，所以带这两个字符的路径
// 拼进去不会报错，只会**表示成别的意思**。实例名会进这些路径，所以必须先判。
func TestMountOptionsSafe(t *testing.T) {
	if !mountOptionsSafe("/opt/asa/umu-prefix", "/opt/asa/umu-prefix-overlay/pve/upper") {
		t.Error("普通路径必须判定为安全")
	}
	for _, bad := range []string{
		"/opt/asa/umu-prefix-overlay/a,b/upper",
		"/opt/asa/umu-prefix-overlay/a:b/upper",
		`/opt/asa/umu-prefix-overlay/a\b/upper`,
		"/opt/asa/umu-prefix-overlay/a\nb/upper",
	} {
		if mountOptionsSafe("/opt/asa/umu-prefix", bad) {
			t.Errorf("含特殊字符的路径必须判定为不安全：%q", bad)
		}
	}
}
