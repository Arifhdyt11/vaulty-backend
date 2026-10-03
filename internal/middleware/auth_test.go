package middleware

import "testing"

func TestBearerToken(t *testing.T) {
	cases := map[string]string{"Bearer abc": "abc", "bearer  abc ": "abc", "Basic abc": "", "": "", "Bearer": ""}
	for in, want := range cases {
		if got := BearerToken(in); got != want {
			t.Errorf("BearerToken(%q) = %q; want %q", in, got, want)
		}
	}
}
