package storage

import "testing"

func TestLooksLikeUUID(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "canonical", value: "00000000-0000-0000-0000-000000000000", want: true},
		{name: "uppercase", value: "A987FBC9-4BED-3078-CF07-9141BA07C9F3", want: true},
		{name: "wrong separators", value: "000000000000000000000000000000000000", want: false},
		{name: "invalid hex", value: "00000000-0000-0000-0000-00000000000g", want: false},
		{name: "account identifier", value: "player-001", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := looksLikeUUID(test.value); got != test.want {
				t.Fatalf("looksLikeUUID(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}
