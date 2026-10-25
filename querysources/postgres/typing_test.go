package postgres

import "testing"

func TestTyping(t *testing.T) {
	for _, c := range typingCases {
		if got := typed(t, c.body); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.body, got, c.want)
		}
	}
}

func TestEdges(t *testing.T) {
	for _, c := range edgeCases {
		if got := typed(t, c.body); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.body, got, c.want)
		}
	}
}
