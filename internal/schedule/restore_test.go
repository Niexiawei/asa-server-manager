package schedule

import (
	"errors"
	"reflect"
	"testing"
)

func TestAppendUnique(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		add   string
		want  []string
	}{
		{"append new", []string{"a", "b"}, "c", []string{"a", "b", "c"}},
		{"duplicate not appended", []string{"a", "b"}, "b", []string{"a", "b"}},
		{"empty slice", nil, "a", []string{"a"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := appendUnique(c.names, c.add)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("appendUnique(%v, %q) = %v, want %v", c.names, c.add, got, c.want)
			}
		})
	}
}

func TestExcludeNames(t *testing.T) {
	cases := []struct {
		name    string
		names   []string
		exclude []string
		want    []string
	}{
		{"exclude intersection", []string{"a", "b", "c"}, []string{"b"}, []string{"a", "c"}},
		{"empty exclude returns original", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"all excluded", []string{"a", "b"}, []string{"a", "b"}, []string{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := excludeNames(c.names, c.exclude)
			if len(got) != len(c.want) {
				t.Fatalf("excludeNames(%v, %v) = %v, want %v", c.names, c.exclude, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("excludeNames(%v, %v) = %v, want %v", c.names, c.exclude, got, c.want)
				}
			}
		})
	}
}

func TestRestoreNote(t *testing.T) {
	cases := []struct {
		name     string
		restored int
		err      error
		want     string
	}{
		{"error present", 0, errors.New("1/2 个实例启动失败：a"), "（恢复启动失败：1/2 个实例启动失败：a）"},
		{"success count", 3, nil, "（已恢复启动 3 个实例）"},
		{"nothing to say", 0, nil, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := restoreNote(c.restored, c.err)
			if got != c.want {
				t.Errorf("restoreNote(%d, %v) = %q, want %q", c.restored, c.err, got, c.want)
			}
		})
	}
}
