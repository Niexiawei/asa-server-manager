package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// BeginRegistration / BeginLogin 返回的 SessionData 必须在 begin 与 finish
// 之间保存下来。
//
// 用内存 map 而不是 Cookie：SessionData 里含 challenge 和 allowCredentials 列表，
// 凭证多的时候可能超过 4KB 的 Cookie 上限。
// 服务重启会丢掉进行中的仪式——用户重试一次即可，可接受。

var ErrCeremonyExpired = errors.New("验证流程已过期，请重试")

const ceremonyTTL = 5 * time.Minute

type ceremony struct {
	data    *webauthn.SessionData
	rpID    string
	userID  int64 // 登录仪式为 0
	expires time.Time
}

var (
	ceremonyMu sync.Mutex
	ceremonies = map[string]*ceremony{}
)

// NewCeremony 存下一次仪式的状态，返回用于取回它的 id
func NewCeremony(rpID string, userID int64, data *webauthn.SessionData) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b)

	ceremonyMu.Lock()
	defer ceremonyMu.Unlock()

	ceremonies[id] = &ceremony{
		data:    data,
		rpID:    rpID,
		userID:  userID,
		expires: time.Now().Add(ceremonyTTL),
	}
	pruneCeremoniesLocked()
	return id, nil
}

// TakeCeremony 取出并**删除**一次仪式的状态。
//
// 取完即删是刻意的：一个 challenge 只能用一次，留着它等于允许重放。
// rpID 必须和当前请求一致——防止在 A 域名发起的仪式被拿到 B 域名去 finish。
func TakeCeremonyWithID(id, rpID string) (*webauthn.SessionData, int64, error) {
	ceremonyMu.Lock()
	defer ceremonyMu.Unlock()

	c, ok := ceremonies[id]
	if !ok {
		return nil, 0, ErrCeremonyExpired
	}
	delete(ceremonies, id)

	if time.Now().After(c.expires) {
		return nil, 0, ErrCeremonyExpired
	}
	if c.rpID != rpID {
		return nil, 0, ErrCeremonyExpired
	}
	return c.data, c.userID, nil
}

func pruneCeremoniesLocked() {
	now := time.Now()
	for k, v := range ceremonies {
		if now.After(v.expires) {
			delete(ceremonies, k)
		}
	}
}
