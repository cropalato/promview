package sources

import "testing"

func TestValidate(t *testing.T) {
	if err := Validate(Source{Slug: "prod-us_1", Name: "Production"}, "0123456789abcdef"); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, test := range []struct {
		name   string
		source Source
		token  string
	}{
		{name: "uppercase slug", source: Source{Slug: "Prod", Name: "Production"}, token: "0123456789abcdef"},
		{name: "empty name", source: Source{Slug: "prod", Name: ""}, token: "0123456789abcdef"},
		{name: "short token", source: Source{Slug: "prod", Name: "Production"}, token: "short"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.source, test.token); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestHashTokenIsDeterministic(t *testing.T) {
	first := HashToken("0123456789abcdef")
	second := HashToken("0123456789abcdef")
	if string(first) != string(second) || string(first) == "0123456789abcdef" {
		t.Fatal("HashToken() did not produce a stable non-plaintext hash")
	}
}
