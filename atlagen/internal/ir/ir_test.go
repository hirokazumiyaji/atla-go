package ir

import "testing"

func TestRequiredForInsert(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Column
		want bool
	}{
		{"not null, no default", Column{}, true},
		{"nullable", Column{Nullable: true}, false},
		{"default", Column{HasDefault: true}, false},
		{"generated", Column{Generated: true}, false},
	} {
		if got := tc.c.RequiredForInsert(); got != tc.want {
			t.Errorf("%s: RequiredForInsert = %v, want %v", tc.name, got, tc.want)
		}
	}
}
