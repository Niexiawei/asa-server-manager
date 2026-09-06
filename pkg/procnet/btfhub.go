package procnet

import (
	"path/filepath"
	"strings"
)

// 这个文件是纯字符串逻辑，**不带 build tag**——btfhub 的目录布局与 os-release 的
// 格式跟平台无关，放在这里才能在 Windows 开发机上单测（procnet_linux.go 里那些
// 依赖 cilium/ebpf 的部分测不了）。

// btfhubCandidates 给出在 btfhub 目录下寻找本机 BTF 的**通配模式**，按优先级排列。
// 调用方对每个模式做一次 filepath.Glob，取第一个能成功解析的文件。
//
// 之所以返回模式而不是确定路径：btfhub-archive 的真实布局是
// <distro>/<version>/<arch>/<release>.btf.tar.xz，而方案 §2.2 当初描述的是
// <distro>/<arch>/<release>.btf；用户也可能只解压了自己那一份、层级更浅。
// 与其赌一种布局，不如按「最精确 → 最宽松」依次试，最后兜底一次全目录通配。
//
// ids 为发行版标识（os-release 的 ID，其后可跟 ID_LIKE 里的各项），空串会被跳过。
func btfhubCandidates(dir, release, arch string, ids []string, versionID string) []string {
	names := []string{release + ".btf", release + ".btf.tar.xz"}

	var out []string
	add := func(parts ...string) {
		for _, name := range names {
			out = append(out, filepath.Join(append(append([]string{dir}, parts...), name)...))
		}
	}

	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if versionID != "" {
			add(id, versionID, arch)
		}
		add(id, arch)
	}

	// 兜底：不认识发行版（或 os-release 读不到）时按层级通配
	add("*", "*", arch)
	add("*", arch)
	add(arch)
	add()
	return out
}

// parseOSRelease 解析 /etc/os-release 的内容，取 ID / VERSION_ID / ID_LIKE。
// 值可能带引号，也可能不带；不认识的行一律忽略。
func parseOSRelease(content string) (id, versionID string, idLike []string) {
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = unquote(strings.TrimSpace(value))
		switch strings.TrimSpace(key) {
		case "ID":
			id = value
		case "VERSION_ID":
			versionID = value
		case "ID_LIKE":
			idLike = strings.Fields(value)
		}
	}
	return id, versionID, idLike
}

// unquote 去掉 os-release 值两端配对的单引号或双引号（两种写法都合法）。
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
