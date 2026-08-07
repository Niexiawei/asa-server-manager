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
		name string
		out  restoreOutcome
		err  error
		want string
	}{
		{"error present", restoreOutcome{}, errors.New("1/2 个实例启动失败：a"), "（恢复启动失败：1/2 个实例启动失败：a）"},
		{"success count", restoreOutcome{Restored: []string{"a", "b", "c"}}, nil, "（已恢复启动 3 个实例）"},
		{"nothing to say", restoreOutcome{}, nil, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := restoreNote(c.out, c.err)
			if got != c.want {
				t.Errorf("restoreNote(%v, %v) = %q, want %q", c.out, c.err, got, c.want)
			}
		})
	}
}

func TestRestoreOutcome_Handled(t *testing.T) {
	out := restoreOutcome{Restored: []string{"a"}, Skipped: []string{"b"}, Failed: []string{"c"}, Cancelled: []string{"d"}}
	got := out.handled()
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("handled() = %v, want %v", got, want)
	}
}

func TestRestoreOutcome_StillOwed(t *testing.T) {
	out := restoreOutcome{Restored: []string{"a"}, Skipped: []string{"b"}, Failed: []string{"c"}, Cancelled: []string{"d"}}
	got := out.stillOwed()
	want := []string{"c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stillOwed() = %v, want %v", got, want)
	}
}
