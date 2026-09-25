package filesyncmanage

import (
	"time"

	"github.com/Niexiawei/simple-file-sync/pkg/joinblob"
)

// CredentialPreview 描述一份引导凭据（不含私钥），供前端在保存前核对"将连接到哪里"。
type CredentialPreview struct {
	Address string `json:"address"`
	// CAFingerprint 与协调端 `coordinator ca show` 打印的格式相同，可以电话/聊天里逐段核对。
	CAFingerprint string    `json:"ca_fingerprint"`
	CertNotAfter  time.Time `json:"cert_not_after"`
}

// InspectJoinBlob 解开一段接入字符串但不保存，返回预览。错误信息是给用户看的。
func InspectJoinBlob(blob string) (CredentialPreview, error) {
	info, err := joinblob.Decode(blob)
	if err != nil {
		return CredentialPreview{}, describeCredentialError(err)
	}
	return preview(info)
}

// ApplyJoinBlob 用接入字符串覆盖 c 的地址与三份引导凭据。
// 字符串里的地址优先于表单里填的：它是协调端自己给出的"Agent 应该连哪里"。
func (c *Config) ApplyJoinBlob(blob string) error {
	info, err := joinblob.Decode(blob)
	if err != nil {
		return describeCredentialError(err)
	}
	c.Address = info.Address
	c.CAPEM, c.BootstrapCertPEM, c.BootstrapKeyPEM = string(info.CAPEM), string(info.CertPEM), string(info.KeyPEM)
	return nil
}

// CredentialPreview 返回当前保存的引导凭据的预览；没有完整的引导凭据时 ok 为 false。
func (c *Config) CredentialPreview() (CredentialPreview, bool) {
	if !c.hasBootstrap() {
		return CredentialPreview{}, false
	}
	p, err := preview(c.bootstrapInfo())
	return p, err == nil
}

func preview(info joinblob.Info) (CredentialPreview, error) {
	summary, err := joinblob.Describe(info)
	if err != nil {
		return CredentialPreview{}, describeCredentialError(err)
	}
	return CredentialPreview{Address: summary.Address, CAFingerprint: summary.CAFingerprint, CertNotAfter: summary.CertNotAfter}, nil
}
