package certmgr

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// Fingerprint 返回证书 DER 的 SHA-1 指纹（大写十六进制），与 Windows
// 证书管理器里显示的「指纹」一致，也是本包做幂等判断的唯一依据。
func Fingerprint(der []byte) string {
	sum := sha1.Sum(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}
