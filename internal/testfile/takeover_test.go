package testfile

import (
	"errors"
	"testing"
)

func TestRemoveTakeOver(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{
			"last field",
			"---\nidentifier: a\nname: A\ntake_over_test_id: 01JABC\n---\n\nDo A.\n",
			"---\nidentifier: a\nname: A\n---\n\nDo A.\n",
		},
		{
			"first field, with a comment and quotes",
			"---\ntake_over_test_id: \"01JABC\" # from pull\n# keep me\nidentifier: a\nname: A\n---\nDo A.\n",
			"---\n# keep me\nidentifier: a\nname: A\n---\nDo A.\n",
		},
		{
			"CRLF and BOM",
			"\xef\xbb\xbf---\r\nidentifier: a\r\ntake_over_test_id: 01JABC\r\nname: A\r\n---\r\nDo A.\r\n",
			"\xef\xbb\xbf---\r\nidentifier: a\r\nname: A\r\n---\r\nDo A.\r\n",
		},
		{
			"the body mentions it",
			"---\nidentifier: a\nname: A\ntake_over_test_id: 01JABC\n---\ntake_over_test_id: is in the body.\n",
			"---\nidentifier: a\nname: A\n---\ntake_over_test_id: is in the body.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RemoveTakeOver("a.md", []byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestRemoveTakeOverRefusesWhatItCantRemoveCleanly(t *testing.T) {
	for name, in := range map[string]string{
		"not set":         "---\nidentifier: a\nname: A\n---\nDo A.\n",
		"invalid file":    "---\nidentifier: a\ntake_over_test_id: 01JABC\n---\n",
		"spans two lines": "---\nidentifier: a\nname: A\ntake_over_test_id:\n  01JABC\n---\nDo A.\n",
		"wrapped value":   "---\nidentifier: a\ntake_over_test_id: 01JABC\n  continued\nname: A\n---\nDo A.\n",
		"flow mapping":    "---\n{identifier: a, name: A, take_over_test_id: 01JABC}\n---\nDo A.\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := RemoveTakeOver("a.md", []byte(in)); !errors.Is(err, ErrCantRemoveTakeOver) {
				t.Errorf("expected ErrCantRemoveTakeOver, got %v and:\n%s", err, got)
			}
		})
	}
}
