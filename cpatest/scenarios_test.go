// Проверки сценариев договора — чистые функции, и доказываются здесь, а не
// стендом: стенд отвечает фикстурами и о ставках оффера не знает ничего.
// Поведение на живой двери доказывает прогон против песочницы.
//
// ⚠ Каждая проверка идёт парой «разрешает верное — запрещает неверное»:
// запрет без разрешения был бы зелёным на проверке, которая отказывает всегда.
package cpatest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.recca.ru/cpa"
)

func fixed(v cpa.RUB) cpa.Rate { return cpa.Rate{Kind: cpa.RateFixed, ValueRUB: &v} }

func percent(p string) cpa.Rate {
	n := json.Number(p)
	return cpa.Rate{Kind: cpa.RatePercent, ValuePercent: &n}
}

func card(revenue, payout, margin cpa.RUB) *cpa.ConversionDetail {
	c := &cpa.ConversionDetail{RevenueRUB: revenue, MarginRUB: margin}
	c.PayoutRUB = payout
	return c
}

func TestPercentOfIsExactOrRefuses(t *testing.T) {
	cases := []struct {
		base    cpa.RUB
		percent string
		want    cpa.RUB
		wantErr bool
	}{
		{10000, "15", 1500, false},
		{10000, "5", 500, false},
		{500, "10", 50, false},
		{10000, "1.5", 150, false},
		// Нецелое — отказ, а не угаданное округление.
		{375, "10", 0, true},
		{100, "abc", 0, true},
	}
	for _, tc := range cases {
		got, err := percentOf(tc.base, tc.percent)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s %% от %s: ждали отказ, получили %s", tc.percent, tc.base, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s %% от %s: %s, %v; ждали %s", tc.percent, tc.base, got, err, tc.want)
		}
	}
}

func TestExpectedRUBByRateKind(t *testing.T) {
	if got, err := expectedRUB(fixed(600), 10000); err != nil || got != 600 {
		t.Errorf("фиксированная: %s, %v", got, err)
	}
	if got, err := expectedRUB(percent("15"), 10000); err != nil || got != 1500 {
		t.Errorf("процентная: %s, %v", got, err)
	}
	for name, r := range map[string]cpa.Rate{
		"фикс без суммы":   {Kind: cpa.RateFixed},
		"процент без доли": {Kind: cpa.RatePercent},
		"вид вне словаря":  {Kind: "bonus"},
	} {
		if _, err := expectedRUB(r, 10000); err == nil {
			t.Errorf("%s: ждали отказ", name)
		}
	}
}

// Сценарий 4 договора: 15 % выручка, 5 % партнёру, 10 % оператору от 10 000.
func TestCheckAmountsRevshare(t *testing.T) {
	if err := checkAmounts(card(1500, 500, 1000), percent("5"), percent("15"), 10000); err != nil {
		t.Fatalf("верный расчёт отбит: %v", err)
	}
	for name, c := range map[string]*cpa.ConversionDetail{
		"выплата на рубль больше": card(1500, 501, 999),
		"выручка на рубль меньше": card(1499, 500, 999),
		"тройка не сходится":      card(1500, 500, 999),
	} {
		if err := checkAmounts(c, percent("5"), percent("15"), 10000); err == nil {
			t.Errorf("%s: прошло", name)
		}
	}
}

func TestCheckAgentLeg(t *testing.T) {
	ok := func() *cpa.ConversionDetail {
		c := card(1500, 500, 1000)
		p := json.Number("10")
		c.AgentLeg = &cpa.AgentLeg{ExternalID: "agent-1", AmountRUB: 50, Base: cpa.AgentLegFromPayout, Percent: &p}
		return c
	}
	if err := checkAgentLeg(ok(), "agent-1", cpa.AgentLegFromPayout, "10"); err != nil {
		t.Fatalf("верная нога отбита: %v", err)
	}

	mutations := map[string]func(*cpa.ConversionDetail){
		"ноги нет":             func(c *cpa.ConversionDetail) { c.AgentLeg = nil },
		"чужой рекрутёр":       func(c *cpa.ConversionDetail) { c.AgentLeg.ExternalID = "agent-2" },
		"не та база":           func(c *cpa.ConversionDetail) { c.AgentLeg.Base = cpa.AgentLegFromMargin },
		"не тот процент":       func(c *cpa.ConversionDetail) { p := json.Number("12"); c.AgentLeg.Percent = &p },
		"процента нет":         func(c *cpa.ConversionDetail) { c.AgentLeg.Percent = nil },
		"сумма не по проценту": func(c *cpa.ConversionDetail) { c.AgentLeg.AmountRUB = 51 },
	}
	for name, mutate := range mutations {
		c := ok()
		mutate(c)
		if err := checkAgentLeg(c, "agent-1", cpa.AgentLegFromPayout, "10"); err == nil {
			t.Errorf("%s: прошло", name)
		}
	}

	// База margin: 10 % от маржи 1000 — 100.
	c := ok()
	c.AgentLeg.Base, c.AgentLeg.AmountRUB = cpa.AgentLegFromMargin, 100
	if err := checkAgentLeg(c, "agent-1", cpa.AgentLegFromMargin, "10"); err != nil {
		t.Errorf("база margin отбита: %v", err)
	}
}

func TestCheckHold(t *testing.T) {
	created := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	withHold := func(h *time.Time) *cpa.ConversionDetail {
		c := card(1, 1, 0)
		c.CreatedAt, c.HoldUntil = created, h
		return c
	}
	good := created.Add(24 * time.Hour)
	if err := checkHold(withHold(&good), 1, created); err != nil {
		t.Fatalf("верный холд отбит: %v", err)
	}
	if err := checkHold(withHold(nil), 1, created); err == nil || !strings.Contains(err.Error(), "не указан") {
		t.Errorf("отсутствующий холд прошёл: %v", err)
	}
	long := created.Add(30 * 24 * time.Hour)
	if err := checkHold(withHold(&long), 1, created); err == nil {
		t.Error("холд оффера вместо холда цели прошёл")
	}
}

// Сценарии без офферов не молчат: каждый назван пропущенным, ошибок нет.
func TestScenariosWithoutOffersAreNamedSkipped(t *testing.T) {
	server := NewServer(t)
	rec := &recorder{}
	Conformance(rec, server.Client(t), ConformanceOptions{Scenarios: &ScenarioOptions{}})
	if len(rec.errors) > 0 {
		t.Fatalf("ошибки: %v", rec.errors)
	}
	skipped := 0
	for _, l := range rec.logs {
		if strings.Contains(l, "сценарий договора") && strings.Contains(l, "пропущен") {
			skipped++
		}
	}
	if skipped != 4 {
		t.Errorf("пропущенными названо %d сценариев из 4:\n  %s", skipped, strings.Join(rec.logs, "\n  "))
	}
}
