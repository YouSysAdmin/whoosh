package ast

import "testing"

func TestPickPrimary(t *testing.T) {
	a := Host{Address: "a"}
	b := Host{Address: "b", Primary: true}
	c := Host{Address: "c", Primary: true}

	cases := []struct {
		name  string
		hosts []Host
		want  string
	}{
		{"no primary falls back to first", []Host{a, {Address: "z"}}, "a"},
		{"primary wins over position", []Host{a, b}, "b"},
		{"first of several primaries wins", []Host{a, b, c}, "b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PickPrimary(tc.hosts)
			if len(got) != 1 || got[0].Address != tc.want {
				t.Errorf("PickPrimary = %v, want single host %q", got, tc.want)
			}
		})
	}

	if got := PickPrimary(nil); got != nil {
		t.Errorf("PickPrimary(nil) = %v, want nil", got)
	}
}
