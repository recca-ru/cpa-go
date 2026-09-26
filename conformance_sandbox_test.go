package cpa_test

import (
	"os"
	"testing"

	"go.recca.ru/cpa"
	"go.recca.ru/cpa/cpatest"
)

// TestConformanceAgainstSandbox гонит приёмочный набор против api-sandbox.recca.ru.
//
// ⚠ Запускает ВЛАДЕЛЕЦ, и только руками: нужен ключ арендатора песочницы, а прогон
// создаёт там данные — партнёра, ссылку, клик, конверсию и заявку на выплату.
// Без ключа тест ПРОПУСКАЕТСЯ с объяснением, а не зеленеет молча: SKIP виден в
// выводе `go test -v`, и «не проверено» не читается как «проверено».
//
// Необязательные переменные:
//
//	RECCA_CPA_SANDBOX_PARTNER      — под каким партнёром идти
//	RECCA_CPA_SANDBOX_OFFER        — на каком оффере
//	RECCA_CPA_SANDBOX_BRAND_OFFER, RECCA_CPA_SANDBOX_BRAND_GOAL — сценарий 1 договора
//	RECCA_CPA_SANDBOX_GOAL_OFFER, RECCA_CPA_SANDBOX_GOAL         — сценарий 3
//	RECCA_CPA_SANDBOX_REVSHARE_OFFER                              — сценарии 2 и 4
//	RECCA_CPA_SANDBOX_SKIP_PAYOUTS — "1", если на площадке нельзя создавать заявки
//	                                 вовсе (пустой баланс цепочку не роняет)
//
// ⚠ Ключ в вывод не попадает: набор его не печатает, а клиент маскирует в отладке.
func TestConformanceAgainstSandbox(t *testing.T) {
	key := os.Getenv("RECCA_CPA_SANDBOX_KEY")
	if key == "" {
		t.Skip("RECCA_CPA_SANDBOX_KEY не задан: прогон против песочницы требует ключа арендатора " +
			"и создаёт там данные, поэтому запускается только руками")
	}

	client, err := cpa.New(key, &cpa.ClientOptions{Environment: cpa.Sandbox})
	if err != nil {
		t.Fatalf("клиент песочницы не создан: %v", err)
	}

	opts := cpatest.ConformanceOptions{
		PartnerExternalID: cpa.PartnerExternalID(os.Getenv("RECCA_CPA_SANDBOX_PARTNER")),
		OfferID:           os.Getenv("RECCA_CPA_SANDBOX_OFFER"),
		SkipPayouts:       os.Getenv("RECCA_CPA_SANDBOX_SKIP_PAYOUTS") == "1",
	}
	// Сценарии приёмки по договору — если заданы их офферы. Без них прогон
	// остаётся проверкой библиотеки и двери, а не приёмкой.
	if s := (cpatest.ScenarioOptions{
		BrandOfferID:    os.Getenv("RECCA_CPA_SANDBOX_BRAND_OFFER"),
		BrandGoal:       os.Getenv("RECCA_CPA_SANDBOX_BRAND_GOAL"),
		GoalOfferID:     os.Getenv("RECCA_CPA_SANDBOX_GOAL_OFFER"),
		Goal:            os.Getenv("RECCA_CPA_SANDBOX_GOAL"),
		RevshareOfferID: os.Getenv("RECCA_CPA_SANDBOX_REVSHARE_OFFER"),
	}); s.BrandOfferID != "" || s.GoalOfferID != "" || s.RevshareOfferID != "" {
		opts.Scenarios = &s
	}
	cpatest.Conformance(t, client, opts)
}
