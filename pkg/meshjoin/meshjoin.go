// Package meshjoin 编解码「一行接入」字符串：join blob（接入协调节点）与以后的邀请码（P3）。
//
// 格式：`<种类>:v<版本>:<base64url(JSON)>.<校验>`，校验 = SHA-256(base64 段) 前 4 字节的 hex。
// 校验和的意义是：粘贴被截断时给出「多半是没复制全」这样明确的报错，而不是解出半截内容
// 再在连接阶段莫名失败。
//
// 自己实现，不引用任何外部 join blob 实现（docs/REMOTE_MANAGER_MESH_PLAN.md §3.1、§8.2）。
// 只依赖标准库与 pkg/meshid。
package meshjoin

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrPrefix：根本不是这种字符串（贴错了东西）。
	ErrPrefix = errors.New("不是有效的接入字符串")
	// ErrVersion：格式版本不认识，多半是对方程序更新、本机需要升级。
	ErrVersion = errors.New("接入字符串的版本不受支持，请升级本程序")
	// ErrChecksum：校验和对不上，多半是粘贴时被截断或改动了。
	ErrChecksum = errors.New("接入字符串校验失败，可能没有复制完整")
	// ErrMalformed：校验和对得上但内容不合法（字段缺失等），通常是生成方的问题。
	ErrMalformed = errors.New("接入字符串内容不完整")
)

const checksumBytes = 4

// encode 把 payload 编成 `<kind>:v<version>:<b64>.<sum>`。
func encode(kind string, version int, payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return kind + ":v" + strconv.Itoa(version) + ":" + body + "." + checksum(body), nil
}

// decode 是 encode 的逆：校验前缀、版本与校验和，再把 JSON 解进 out。
func decode(s, kind string, version int, out any) error {
	s = strings.TrimSpace(s)
	rest, ok := strings.CutPrefix(s, kind+":")
	if !ok {
		return ErrPrefix
	}
	ver, body, ok := strings.Cut(rest, ":")
	if !ok || !strings.HasPrefix(ver, "v") {
		return ErrPrefix
	}
	if n, err := strconv.Atoi(ver[1:]); err != nil || n != version {
		return fmt.Errorf("%w（%s）", ErrVersion, ver)
	}
	b64, sum, ok := strings.Cut(body, ".")
	if !ok || checksum(b64) != strings.ToLower(sum) {
		return ErrChecksum
	}
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return ErrChecksum
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return nil
}

func checksum(b64 string) string {
	sum := sha256.Sum256([]byte(b64))
	return hex.EncodeToString(sum[:checksumBytes])
}
