package testfile

import (
	"reflect"
	"testing"
)

func TestFormatRoundTrips(t *testing.T) {
	yes, no := true, false
	tests := []Test{
		{
			Identifier:           "sign-in",
			Name:                 "Sign in with email",
			Description:          "Open the app.\n\n---\n\nSign in.",
			Folder:               "Smoke",
			Draft:                &yes,
			Tags:                 []string{"smoke", "regression"},
			Secrets:              []string{"test-account"},
			DisabledCapabilities: []string{"audio_input"},
			TakeOverTestID:       "01JABC",
		},
		{
			// Values YAML would otherwise read as something other than text.
			Identifier:  "404",
			Name:        "true: or #not, a 'comment' \"here\"",
			Description: "- a list item\n- another",
			Folder:      "null",
			Draft:       &no,
			Tags:        []string{"yes", "1.5", "[x]"},
			Secrets:     []string{},
		},
		{
			Identifier:  "cafe",
			Name:        "Café ☕",
			Description: "Order.",
			Draft:       &no,
		},
	}
	for _, want := range tests {
		data := Format(want)
		got, errs := Parse("test.md", data)
		if len(errs) > 0 {
			t.Fatalf("%s doesn't parse: %v\n%s", want.Identifier, errs, data)
		}
		got.Path, got.identifierLine = "", 0
		// A left-out tags or disabled_capabilities means none either way.
		if want.Tags == nil {
			want.Tags = []string{}
		}
		if want.DisabledCapabilities == nil {
			want.DisabledCapabilities = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got:\n%#v\nwant:\n%#v\nfile:\n%s", got, want, data)
		}
	}
}

func TestFormatLeavesOutUnsetSecrets(t *testing.T) {
	no := false
	got := string(Format(Test{Identifier: "a", Name: "A", Description: "Do A.", Draft: &no}))
	want := "---\nidentifier: a\nname: A\ndraft: false\ntags: []\ndisabled_capabilities: []\n---\n\nDo A.\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSlug(t *testing.T) {
	for name, want := range map[string]string{
		"Sign in with email":      "sign-in-with-email",
		"  Checkout -- referral ": "checkout-referral",
		"Café crème":              "cafe-creme",
		"iOS 18: Face ID":         "ios-18-face-id",
		"日本語":                     "",
		"":                        "",
	} {
		if got := Slug(name); got != want {
			t.Errorf("Slug(%q) = %q, want %q", name, got, want)
		}
	}
}
