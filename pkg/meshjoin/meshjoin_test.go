package meshjoin

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"asa-server/pkg/meshid"
)

const secret = "s3cr3t-network-key-do-not-log"

func sampleBlob(t *testing.T) JoinBlob {
	t.Helper()
	key, err := meshid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	id, err := meshid.FromPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return JoinBlob{Addr: "coord.example.com:443", Coordinator: id, NetworkID: "default", NetworkSecret: secret}
}

func TestJoinBlobRoundTrip(t *testing.T) {
	j := sampleBlob(t)
	s, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "asa-mesh-join:v1:") {
		t.Fatalf("前缀不对：%s", s)
	}
	got, err := ParseJoinBlob("  " + s + "\r\n") // 粘贴常带首尾空白
	if err != nil {
		t.Fatal(err)
	}
	if got != j {
		t.Fatalf("往返不一致：%#v vs %#v", got, j)
	}
}

func TestJoinBlobErrors(t *testing.T) {
	s, err := sampleBlob(t).Encode()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"空", "", ErrPrefix},
		{"别的东西", "https://example.com", ErrPrefix},
		{"邀请码不是 join blob", strings.Replace(s, "asa-mesh-join:", "asa-mesh-invite:", 1), ErrPrefix},
		{"版本", strings.Replace(s, ":v1:", ":v2:", 1), ErrVersion},
		{"截断", s[:len(s)-20], ErrChecksum},
		{"截掉校验", s[:strings.LastIndex(s, ".")], ErrChecksum},
		{"改了一个字符", s[:30] + flip(s[30]) + s[31:], ErrChecksum},
	}
	for _, c := range cases {
		if _, err := ParseJoinBlob(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v，应为 %v", c.name, err, c.want)
		}
	}
}

func flip(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}

func TestJoinBlobValidate(t *testing.T) {
	j := sampleBlob(t)
	bad := []func(*JoinBlob){
		func(j *JoinBlob) { j.Addr = "" },
		func(j *JoinBlob) { j.Addr = "no-port" },
		func(j *JoinBlob) { j.Coordinator = meshid.ID{} },
		func(j *JoinBlob) { j.NetworkID = "" },
		func(j *JoinBlob) { j.NetworkSecret = "" },
	}
	for i, mutate := range bad {
		b := j
		mutate(&b)
		if _, err := b.Encode(); !errors.Is(err, ErrMalformed) {
			t.Errorf("case %d: err = %v，应为 ErrMalformed", i, err)
		}
	}
}

func TestJoinBlobNeverPrintsSecret(t *testing.T) {
	j := sampleBlob(t)
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if out := fmt.Sprintf(verb, j); strings.Contains(out, secret) {
			t.Errorf("%s 输出了密钥：%s", verb, out)
		}
		if out := fmt.Sprintf(verb, &j); strings.Contains(out, secret) {
			t.Errorf("%s（指针）输出了密钥：%s", verb, out)
		}
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("join", "blob", j)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("join", "blob", j)
	if strings.Contains(buf.String(), secret) {
		t.Errorf("slog 输出了密钥：%s", buf.String())
	}
	if !strings.Contains(buf.String(), "coord.example.com") {
		t.Errorf("slog 输出应包含非敏感字段：%s", buf.String())
	}
}
