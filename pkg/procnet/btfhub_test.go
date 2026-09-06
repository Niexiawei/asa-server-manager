package procnet

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	const content = `# 注释行，含 = 号也不该被当成键值
NAME="Ubuntu"
VERSION_ID="20.04"
ID=ubuntu
ID_LIKE=debian
PRETTY_NAME='Ubuntu 20.04.6 LTS'
`
	id, versionID, idLike := parseOSRelease(content)
	if id != "ubuntu" {
		t.Errorf("ID = %q, want ubuntu", id)
	}
	if versionID != "20.04" {
		t.Errorf("VERSION_ID = %q, want 20.04（引号要去掉）", versionID)
	}
	if !slices.Equal(idLike, []string{"debian"}) {
		t.Errorf("ID_LIKE = %v, want [debian]", idLike)
	}
}

func TestParseOSReleaseMultipleIDLike(t *testing.T) {
	_, _, idLike := parseOSRelease("ID=rocky\nID_LIKE=\"rhel centos fedora\"\n")
	if !slices.Equal(idLike, []string{"rhel", "centos", "fedora"}) {
		t.Errorf("ID_LIKE = %v, want [rhel centos fedora]", idLike)
	}
}

func TestParseOSReleaseEmpty(t *testing.T) {
	id, versionID, idLike := parseOSRelease("")
	if id != "" || versionID != "" || idLike != nil {
		t.Errorf("空内容应当全为零值，得到 %q %q %v", id, versionID, idLike)
	}
}

func TestBTFHubCandidatesOrder(t *testing.T) {
	got := btfhubCandidates("/btfhub", "5.4.0-42-generic", "x86_64", []string{"ubuntu", "debian"}, "20.04")

	// btfhub-archive 的真实布局（distro/version/arch）必须排在最前
	want0 := filepath.Join("/btfhub", "ubuntu", "20.04", "x86_64", "5.4.0-42-generic.btf")
	want1 := filepath.Join("/btfhub", "ubuntu", "20.04", "x86_64", "5.4.0-42-generic.btf.tar.xz")
	if got[0] != want0 || got[1] != want1 {
		t.Fatalf("前两个候选 = %q / %q, want %q / %q", got[0], got[1], want0, want1)
	}

	mustContain := []string{
		// 方案 §2.2 描述的浅一层布局
		filepath.Join("/btfhub", "ubuntu", "x86_64", "5.4.0-42-generic.btf"),
		// ID_LIKE 也要试
		filepath.Join("/btfhub", "debian", "20.04", "x86_64", "5.4.0-42-generic.btf"),
		// 认不出发行版时的通配兜底
		filepath.Join("/btfhub", "*", "*", "x86_64", "5.4.0-42-generic.btf"),
		filepath.Join("/btfhub", "*", "x86_64", "5.4.0-42-generic.btf"),
		// 用户只把自己那一份丢在目录根下
		filepath.Join("/btfhub", "5.4.0-42-generic.btf"),
	}
	for _, want := range mustContain {
		if !slices.Contains(got, want) {
			t.Errorf("候选里缺少 %q", want)
		}
	}
}

func TestBTFHubCandidatesSkipsEmptyAndDuplicateIDs(t *testing.T) {
	// os-release 读不到时 ids 里会有空串，它不该多产生任何候选
	// （注意不能直接比较路径：filepath.Join 会把空段清掉，拼出来的
	//  /btfhub/x86_64/... 恰好与最后那条兜底候选重合）
	got := btfhubCandidates("/btfhub", "5.4.0", "x86_64", []string{"", "ubuntu", "ubuntu"}, "")
	base := btfhubCandidates("/btfhub", "5.4.0", "x86_64", []string{"ubuntu"}, "")
	if !slices.Equal(got, base) {
		t.Errorf("空串与重复的发行版 ID 应当被跳过：\n got  = %v\n want = %v", got, base)
	}

	var ubuntu int
	for _, p := range got {
		if p == filepath.Join("/btfhub", "ubuntu", "x86_64", "5.4.0.btf") {
			ubuntu++
		}
	}
	if ubuntu != 1 {
		t.Errorf("重复的发行版 ID 应当只产生一组候选，得到 %d 次", ubuntu)
	}
}

func TestBTFHubCandidatesWithoutVersionID(t *testing.T) {
	got := btfhubCandidates("/btfhub", "5.4.0", "x86_64", []string{"arch"}, "")
	bad := filepath.Join("/btfhub", "arch", "x86_64", "5.4.0.btf")
	if !slices.Contains(got, bad) {
		t.Errorf("没有 VERSION_ID 时也要给出 <distro>/<arch> 这一层，缺 %q", bad)
	}
}
