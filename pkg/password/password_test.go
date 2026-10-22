package password

import "testing"

func TestHashVerify(t *testing.T) {
	h, err := Hash("rahasia123")
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify("rahasia123", h); err != nil {
		t.Fatal("harus cocok")
	}
	if err := Verify("salah", h); err == nil {
		t.Fatal("harus gagal")
	}
	if err := Verify("x", "bukan-hash"); err == nil {
		t.Fatal("format rusak harus gagal")
	}
}
