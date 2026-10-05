// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package ted

import (
	"os"
	"testing"
)

func loadSample(t *testing.T) map[string]Notice {
	t.Helper()
	data, err := os.ReadFile("testdata/search_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ParseSearchResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Notice{}
	for _, raw := range resp.Notices {
		n := Extract(raw)
		out[n.ID] = n
	}
	return out
}

func TestExtractWinnersAlignsContactsWithTenderers(t *testing.T) {
	n := loadSample(t)["680471-2026"]
	if len(n.Winners) != 3 {
		t.Fatalf("want 3 winners, got %d: %+v", len(n.Winners), n.Winners)
	}
	byName := map[string]Winner{}
	for _, w := range n.Winners {
		byName[w.Name] = w
	}
	cases := []struct{ name, city, email, id string }{
		{"Johann Bunte", "Kelsterbach", "winner-bunte@example.com", "DE 116961471"},
		{"SECUTEC GmbH", "Mannheim", "winner-secutec@example.com", "DE 140211424"},
		{"SP - Fahrbahnmarkierung GmbH", "Wehrheim", "winner-markierung@example.com", "DE313414584"},
	}
	for _, c := range cases {
		w, ok := byName[c.name]
		if !ok {
			t.Fatalf("missing winner %q", c.name)
		}
		if w.City != c.city || w.Email != c.email || w.Identifier != c.id {
			t.Errorf("%s: got city=%q email=%q id=%q", c.name, w.City, w.Email, w.Identifier)
		}
		if w.Country != "DEU" || w.LotsWon != 1 {
			t.Errorf("%s: country=%q lots=%d", c.name, w.Country, w.LotsWon)
		}
	}
}

func TestExtractWinnersSumsPerLotValues(t *testing.T) {
	n := loadSample(t)["679898-2026"]
	if len(n.Winners) != 12 {
		t.Fatalf("want 12 distinct companies, got %d", len(n.Winners))
	}
	for _, w := range n.Winners {
		if w.Name == "SAS REGIS PERE ET FILS" {
			if w.LotsWon != 2 {
				t.Errorf("REGIS lots=%d, want 2", w.LotsWon)
			}
			if w.Value <= 217494.75 {
				t.Errorf("REGIS value=%v, want sum of two lots", w.Value)
			}
			if w.City != "PUGET SUR ARGENS" {
				t.Errorf("REGIS city=%q", w.City)
			}
		}
	}
	if n.ContractValue != 5431556.8 {
		t.Errorf("contract value=%v", n.ContractValue)
	}
}

func TestExtractCallNotice(t *testing.T) {
	n := loadSample(t)["679227-2026"]
	if n.NoticeType != NoticeTypeCall {
		t.Errorf("type=%q", n.NoticeType)
	}
	if n.Title != "Landschaftsgärtnerische Arbeiten" {
		t.Errorf("title=%q", n.Title)
	}
	if n.EstimatedValue != 1036573.53 || n.SubmissionDeadline != "2026-11-04" {
		t.Errorf("value=%v deadline=%q", n.EstimatedValue, n.SubmissionDeadline)
	}
	if n.BuyerCity != "Berlin" || n.PlaceOfPerformance != "DE300" || n.PublicationDate != "2026-10-02" {
		t.Errorf("city=%q nuts=%q date=%q", n.BuyerCity, n.PlaceOfPerformance, n.PublicationDate)
	}
	if len(n.Winners) != 0 {
		t.Errorf("call notice should have no winners, got %d", len(n.Winners))
	}
}

func TestListShapes(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"scalar", "x", "x"},
		{"array", []any{"a", "b"}, "a"},
		{"lang scalar", map[string]any{"deu": "Bau"}, "Bau"},
		{"lang array", map[string]any{"pol": []any{"Budowa"}}, "Budowa"},
		{"prefers eng", map[string]any{"fra": "Travaux", "eng": "Works"}, "Works"},
		{"mul", map[string]any{"mul": []any{"Berlin"}}, "Berlin"},
		{"number", 12.5, "12.5"},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		if got := Text(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestResolveTitleFallsBackToNoticeTitle(t *testing.T) {
	raw := map[string]any{"notice-title": map[string]any{"deu": "Deutschland – Bauarbeiten – Neubau Schule"}}
	if got := ResolveTitle(raw); got != "Neubau Schule" {
		t.Errorf("got %q", got)
	}
	raw["title-proc"] = map[string]any{"fra": "Signalisation routière"}
	raw["title-lot"] = map[string]any{"fra": []any{"EPV/2026/03/TP - 1"}}
	if got := ResolveTitle(raw); got != "Signalisation routière" {
		t.Errorf("title-proc should win, got %q", got)
	}
}

func TestBuildQuery(t *testing.T) {
	cases := []struct {
		name string
		f    Filter
		want string
	}{
		{"empty", Filter{}, ""},
		{"country cpv", Filter{Country: "deu", CPV: "45"}, "buyer-country=DEU AND classification-cpv=45000000 SORT BY publication-date DESC"},
		{"types since", Filter{NoticeTypes: []string{NoticeTypeCall, NoticeTypeAward}, Since: "2026-09-01"}, "notice-type IN (cn-standard can-standard) AND publication-date>=20260901 SORT BY publication-date DESC"},
		{"raw query", Filter{Query: "buyer-name~Berlin", Country: "DEU"}, "(buyer-name~Berlin) AND buyer-country=DEU SORT BY publication-date DESC"},
	}
	for _, c := range cases {
		if got := BuildQuery(c.f); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestCPVHelpers(t *testing.T) {
	cases := []struct{ in, norm, prefix string }{
		{"45", "45000000", "45"},
		{"4523", "45230000", "4523"},
		{"45210000-2", "45210000", "4521"},
		{"72000000", "72000000", "72"},
	}
	for _, c := range cases {
		if got := NormalizeCPV(c.in); got != c.norm {
			t.Errorf("NormalizeCPV(%q)=%q", c.in, got)
		}
		if got := CPVPrefix(c.in); got != c.prefix {
			t.Errorf("CPVPrefix(%q)=%q", c.in, got)
		}
	}
}

func TestPrimaryCPVMatches(t *testing.T) {
	n := Notice{CPVCode: "66172000", CPVCodes: []string{"66172000", "45259000"}}
	if PrimaryCPVMatches(n, "45") {
		t.Error("secondary-only construction code must not match")
	}
	if !PrimaryCPVMatches(n, "66") || !PrimaryCPVMatches(n, "") {
		t.Error("primary prefix and empty filter must match")
	}
}

func TestPublicationQuery(t *testing.T) {
	if got := PublicationQuery("680471-2026"); got != "publication-number=680471-2026" {
		t.Fatalf("PublicationQuery = %q", got)
	}
}

func TestNumberSkipsZeroAndNegative(t *testing.T) {
	if got := Number([]any{"0", "-5", "12.5", "7"}); got != 12.5 {
		t.Fatalf("Number = %v, want 12.5", got)
	}
}
