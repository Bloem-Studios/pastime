package bloem

import "testing"

func TestListFollowLocators(t *testing.T) {
	for _, tc := range []struct{ url, kind, id, user, slug string }{
		{"https://pastime.example/lists/42", "personal", "42", "", ""},
		{"https://pastime.example/collab/8", "collaborative", "8", "", ""},
		{"https://pastime.example/u/sam/weekend", "public", "", "sam", "weekend"},
		{"https://pastime.example/api/v1/public/sam/lists/weekend", "public", "", "sam", "weekend"},
	} {
		got, err := ParseListLocator("https://pastime.example", tc.url)
		if err != nil || got.Kind != tc.kind || got.NativeID != tc.id || got.Username != tc.user || got.Slug != tc.slug {
			t.Fatal(got, err)
		}
	}
}

func TestListFollowRejectsUnsafeLocators(t *testing.T) {
	for _, link := range []string{
		"https://other.example/lists/42", "http://pastime.example/lists/42",
		"https://user:pass@pastime.example/lists/42", "https://pastime.example/lists/42#secret",
		"https://pastime.example/lists/42?key=secret", "https://pastime.example/lists/0042",
		"https://pastime.example/lists/-1", "https://pastime.example/lists/9223372036854775808",
		"https://pastime.example/u/sam/weekend/extra", "https://pastime.example/u/sam/%2fweekend",
		"https://pastime.example/u/sam/%2e%2e", "https://pastime.example/u/sam/weekend%0a",
		"/lists/42", "https://pastime.example:443/lists/42", "https://pastime.example/lists/42/",
	} {
		if _, err := ParseListLocator("https://pastime.example", link); err == nil {
			t.Fatalf("accepted %q", link)
		}
	}
}
