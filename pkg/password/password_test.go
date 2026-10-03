package password

import "testing"

func TestRoundTrip(t *testing.T) {
	hash, err := Hash("rahasia-panjang")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := Verify("rahasia-panjang", hash); !ok || err != nil {
		t.Fatalf("password benar ditolak: %v", err)
	}
	if ok, _ := Verify("salah", hash); ok {
		t.Fatal("password salah diterima")
	}
	if _, err := Verify("x", "bukan-hash"); err == nil {
		t.Fatal("hash rusak harus error")
	}
}
