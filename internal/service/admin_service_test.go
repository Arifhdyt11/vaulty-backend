package service

import (
	"testing"
	"time"
)

func TestAturanPlanBolakBalikTanpaMengubahPrioritasAntrian(t *testing.T) {
	for p, r := range defaultPlanRules {
		got := ConfigFromRules(r).apply(r)
		if got != r {
			t.Fatalf("%s: aturan berubah setelah bolak-balik: %+v != %+v", p, got, r)
		}
	}
	c := ConfigFromRules(defaultPlanRules["free"])
	c.STTMaxMinutes, c.AISuggestReminds, c.ChatHistoryDays = 10, true, 0
	got := c.apply(defaultPlanRules["free"])
	if got.STTMaxMinutes != 10 || !got.AISuggestReminds || got.ChatHistory != 0 || got.QueuePriority != "default" {
		t.Fatalf("override tidak terpasang benar: %+v", got)
	}
}

func TestRulesForMemakaiOverrideAdmin(t *testing.T) {
	t.Cleanup(func() { activeRules.Store(nil) })
	if RulesFor("free").STTMaxMinutes != 0 {
		t.Fatal("default free seharusnya tanpa transkrip")
	}
	over := map[string]PlanRules{"free": defaultPlanRules["free"], "pro": defaultPlanRules["pro"]}
	f := over["free"]
	f.STTMaxMinutes, f.TrashRetention = 15, 14*24*time.Hour
	over["free"] = f
	SetPlanRules(over)
	if RulesFor("free").STTMaxMinutes != 15 || RulesFor("tidak-ada").STTMaxMinutes != 15 {
		t.Fatalf("override tidak dipakai: %+v", RulesFor("free"))
	}
	if MaxUploadBytes("pro") != defaultPlanRules["pro"].MaxUploadMB<<20 {
		t.Fatal("aturan pro ikut berubah")
	}
}

func TestKodeKuponDinormalisasiDanDivalidasi(t *testing.T) {
	cases := map[string]bool{" temanvee ": true, "PRO-30": true, "A_1": true, "ab": false, "spasi di": false, "-AWAL": false, "😀😀😀": false}
	for in, want := range cases {
		if got := couponCode.MatchString(NormalizeCoupon(in)); got != want {
			t.Errorf("%q: valid=%v, mau %v", in, got, want)
		}
	}
}
