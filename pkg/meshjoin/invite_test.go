package meshjoin

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"asa-server/pkg/meshid"
)

func sampleInvite(t *testing.T) Invite {
	t.Helper()
	key, err := meshid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	id, err := meshid.FromPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return Invite{
		Node: id, ID: "inv-1", Secret: bytes.Repeat([]byte{0xAB}, InviteSecretLen), Role: "operator",
		Addrs: []string{"192.168.1.20:19194"}, Expires: time.Unix(1_900_000_000, 0),
	}
}

func TestInviteRoundTrip(t *testing.T) {
	v := sampleInvite(t)
	s, err := v.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "asa-mesh-invite:v1:") {
		t.Fatalf("前缀不对：%s", s)
	}
	got, err := ParseInvite(" " + s + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Node != v.Node || got.ID != v.ID || !bytes.Equal(got.Secret, v.Secret) || got.Role != v.Role ||
		len(got.Addrs) != 1 || got.Addrs[0] != v.Addrs[0] || !got.Expires.Equal(v.Expires) {
		t.Fatalf("往返不一致：%#v", got)
	}
}

func TestInviteErrors(t *testing.T) {
	s, err := sampleInvite(t).Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		in   string
		want error
	}{
		{"join blob 不是邀请码", strings.Replace(s, "asa-mesh-invite:", "asa-mesh-join:", 1), ErrPrefix},
		{"版本", strings.Replace(s, ":v1:", ":v9:", 1), ErrVersion},
		{"截断", s[:len(s)-10], ErrChecksum},
	} {
		if _, err := ParseInvite(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v，应为 %v", c.name, err, c.want)
		}
	}

	bad := sampleInvite(t)
	bad.Secret = bad.Secret[:5]
	if _, err := bad.Encode(); !errors.Is(err, ErrMalformed) {
		t.Fatalf("密钥长度不对应拒绝编码，得到 %v", err)
	}
	bad = sampleInvite(t)
	bad.Addrs = []string{"no-port"}
	if _, err := bad.Encode(); !errors.Is(err, ErrMalformed) {
		t.Fatalf("地址不是 host:port 应拒绝编码，得到 %v", err)
	}
}

func TestInviteRedacted(t *testing.T) {
	v := sampleInvite(t)
	v.Secret = []byte(strings.Repeat("Z", InviteSecretLen))
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if out := fmt.Sprintf(verb, v); strings.Contains(out, "ZZZZ") {
			t.Errorf("%s 泄露了密钥：%s", verb, out)
		}
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "invite", v)
	if strings.Contains(buf.String(), "ZZZZ") {
		t.Fatalf("slog 泄露了密钥：%s", buf.String())
	}
}
