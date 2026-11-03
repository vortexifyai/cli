package pull

import "testing"

func TestDetermineStatus(t *testing.T) {
	cases := []struct {
		local, base, remote string
		want                Status
	}{{"a", "a", "a", StatusClean}, {"a", "a", "b", StatusServerAhead}, {"b", "a", "a", StatusLocalAhead}, {"b", "a", "c", StatusConflict}, {"b", "a", "b", StatusClean}, {"b", "", "c", StatusConflict}}
	for _, c := range cases {
		if got := DetermineStatus(c.local, c.base, c.remote); got != c.want {
			t.Errorf("%+v got %s", c, got)
		}
	}
}
