package cpatest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"go.recca.ru/cpa"
)

// ScenarioOptions — сценарии приёмки из дополнения к Приложению № 1 договора
// (раздел 6). Каждый сценарий идёт на СВОЁМ оффере, потому что доказывает
// свойство оффера: цель «регистрация бренда», ставку конкретной цели, долю
// revshare. Сценарий с пустым оффером пропускается и называется в журнале
// пропущенным — молча он не выпадает.
//
// ⚠ Ожидаемые суммы набор ВЫВОДИТ из условий оффера, прочитанных у той же
// двери, а не берёт из договора: одна и та же библиотека приёмки годится для
// любой сети, а цифры договора (15 % / 5 %) проверяются тем, что в протокол
// попадают вместе с ответом двери.
type ScenarioOptions struct {
	// BrandOfferID и BrandGoal — сценарий 1: оффер с целью «регистрация бренда».
	BrandOfferID string
	BrandGoal    string
	// AgentPercent — сценарий 2: ставка рекрутёра, с которой набор его заводит.
	// Пусто — "10". Рекрутёр и вебмастер заводятся на оффере RevshareOfferID.
	AgentPercent string
	// GoalOfferID и Goal — сценарий 3: оффер с целями, у каждой своя ставка.
	GoalOfferID string
	Goal        string
	// RevshareOfferID — сценарий 4 (и площадка сценария 2).
	RevshareOfferID string
	// SumRUB — сумма заказа для процентных ставок. Пусто — 10000: сумма, при
	// которой доли договора (15 %, 5 %, 10 %) дают целые рубли.
	SumRUB cpa.RUB
}

func (s ScenarioOptions) sum() cpa.RUB {
	if s.SumRUB > 0 {
		return s.SumRUB
	}
	return 10000
}

func (s ScenarioOptions) agentPercent() string {
	if s.AgentPercent != "" {
		return s.AgentPercent
	}
	return "10"
}

// runScenarios гоняет сценарии договора. Каждый независим: падение одного не
// прячет остальные.
func runScenarios(ctx context.Context, t TB, c *cpa.Client, opts ConformanceOptions) {
	s := *opts.Scenarios
	stamp := fmt.Sprint(opts.now().Unix())

	type scenario struct {
		name    string
		offerID string
		run     func() error
	}
	all := []scenario{
		{"1 (бренды и блогеры)", s.BrandOfferID, func() error { return scenarioBrand(ctx, t, c, opts, stamp) }},
		{"2 (скаут)", s.RevshareOfferID, func() error { return scenarioScout(ctx, t, c, opts, stamp) }},
		{"3 (целевое действие)", s.GoalOfferID, func() error { return scenarioGoal(ctx, t, c, opts, stamp) }},
		{"4 (revshare)", s.RevshareOfferID, func() error { return scenarioRevshare(ctx, t, c, opts, stamp) }},
	}
	for _, sc := range all {
		if sc.offerID == "" {
			t.Logf("сценарий договора %s пропущен: оффер не задан", sc.name)
			continue
		}
		if err := sc.run(); err != nil {
			t.Errorf("сценарий договора %s: %v", sc.name, err)
		}
	}
}

// ── общие шаги ───────────────────────────────────────────────────────────────

// converted — итог сквозной операции «ссылка → клик → конверсия → карточка».
type converted struct {
	card      *cpa.ConversionDetail
	requestID string // request_id приёма конверсии — главный для протокола
}

// convert проводит сквозную операцию и пишет request_id каждого шага: протокол
// приёмки по договору обязан содержать идентификатор каждого запроса.
func convert(ctx context.Context, t TB, c *cpa.Client, step string, partner cpa.PartnerExternalID,
	offerID string, goal *string, sum *cpa.RUB, externalID, clickKey string) (*converted, error) {
	link, resp, err := c.Links.Create(ctx, cpa.CreateLinkParams{PartnerExternalID: partner, OfferID: offerID})
	if err != nil {
		return nil, fmt.Errorf("ссылка: %w", err)
	}
	t.Logf("  %s · ссылка %s — request_id %s", step, link.Code, resp.RequestID)

	click, resp, err := c.Clicks.Create(ctx, cpa.CreateClickParams{
		PartnerExternalID: partner, OfferID: offerID, ClientClickID: clickKey, Country: "RU",
	})
	if err != nil {
		return nil, fmt.Errorf("переход: %w", err)
	}
	t.Logf("  %s · переход — request_id %s", step, resp.RequestID)
	if click.Status == cpa.ClickTrafficback || click.ClickID == nil {
		return nil, fmt.Errorf("переход отбит (%s) — сценарий дальше не идёт",
			valueOr(click.Reason, "click_id не выдан"))
	}

	conversion, resp, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
		ClickID: *click.ClickID, ExternalID: externalID, Status: "approved", Goal: goal, SumRUB: sum,
	})
	if err != nil {
		return nil, fmt.Errorf("конверсия: %w", err)
	}
	t.Logf("  %s · конверсия %s — request_id %s", step, conversion.ExternalID, resp.RequestID)
	created := resp.RequestID

	card, resp, err := c.Conversions.Get(ctx, cpa.GetConversionParams{ExternalID: externalID, Goal: goal})
	if err != nil {
		return nil, fmt.Errorf("карточка конверсии (метод 13): %w", err)
	}
	t.Logf("  %s · карточка (метод 13): выручка %s = выплата %s + маржа %s — request_id %s",
		step, card.RevenueRUB, card.PayoutRUB, card.MarginRUB, resp.RequestID)
	if err := checkTriple(card.RevenueRUB, card.PayoutRUB, card.MarginRUB); err != nil {
		return nil, err
	}
	return &converted{card: card, requestID: created}, nil
}

func upsertWebmaster(ctx context.Context, t TB, c *cpa.Client, step string, id cpa.PartnerExternalID) error {
	_, resp, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
		ExternalID: id, Role: cpa.PartnerWebmaster, PartnerState: cpa.PartnerStateActive,
	})
	if err != nil {
		return fmt.Errorf("партнёр %s: %w", id, err)
	}
	t.Logf("  %s · партнёр %s (метод 2) — request_id %s", step, id, resp.RequestID)
	return nil
}

// ── сценарий 1: бренды и блогеры ─────────────────────────────────────────────

func scenarioBrand(ctx context.Context, t TB, c *cpa.Client, opts ConformanceOptions, stamp string) error {
	s := opts.Scenarios
	if s.BrandGoal == "" {
		return errors.New("не задана цель «регистрация бренда» (BrandGoal)")
	}
	partner := cpa.PartnerExternalID(opts.prefix() + "s1-referrer")
	if err := upsertWebmaster(ctx, t, c, "С1", partner); err != nil {
		return err
	}
	pay, rev, err := goalRates(ctx, c, s.BrandOfferID, s.BrandGoal)
	if err != nil {
		return err
	}
	sum := s.sum()
	got, err := convert(ctx, t, c, "С1", partner, s.BrandOfferID, &s.BrandGoal, &sum,
		opts.prefix()+"s1-order-"+stamp, opts.prefix()+"s1-click-"+stamp)
	if err != nil {
		return err
	}
	if err := checkAmounts(got.card, pay, rev, sum); err != nil {
		return err
	}
	if got.card.PartnerExternalID == nil || *got.card.PartnerExternalID != partner {
		return fmt.Errorf("приведший указан неверно: %v, ждали %s", ptrString(got.card.PartnerExternalID), partner)
	}
	t.Logf("сценарий договора 1 пройден: цель %s, приведший %s, выручка %s = выплата %s + маржа %s",
		got.card.Goal, partner, got.card.RevenueRUB, got.card.PayoutRUB, got.card.MarginRUB)
	return nil
}

// ── сценарий 2: скаут ────────────────────────────────────────────────────────

func scenarioScout(ctx context.Context, t TB, c *cpa.Client, opts ConformanceOptions, stamp string) error {
	s := opts.Scenarios
	self, resp, err := c.Self.Get(ctx)
	if err != nil {
		return fmt.Errorf("база ноги сети (метод 1): %w", err)
	}
	t.Logf("  С2 · база ноги рекрутёра у сети: %s — request_id %s", self.AgentBase, resp.RequestID)

	// ⚠ Рекрутёр свой на каждый прогон: ставку действующего агента дверь на
	// повторе не меняет (400), и прогон с другой AgentPercent упёрся бы в отказ.
	agent := cpa.PartnerExternalID(opts.prefix() + "s2-agent-" + stamp)
	percent := json.Number(s.agentPercent())
	_, resp, err = c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
		ExternalID: agent, Role: cpa.PartnerAgent, AgentPercent: &percent,
	})
	if err != nil {
		return fmt.Errorf("рекрутёр: %w", err)
	}
	t.Logf("  С2 · рекрутёр %s, ставка %s %% (метод 2) — request_id %s", agent, percent, resp.RequestID)

	web := cpa.PartnerExternalID(opts.prefix() + "s2-webmaster-" + stamp)
	_, resp, err = c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
		ExternalID: web, Role: cpa.PartnerWebmaster, RecruiterExternalID: agent,
	})
	if err != nil {
		return fmt.Errorf("вебмастер с рекрутёром: %w", err)
	}
	t.Logf("  С2 · вебмастер %s с рекрутёром %s (метод 2) — request_id %s", web, agent, resp.RequestID)

	sum := s.sum()
	got, err := convert(ctx, t, c, "С2", web, s.RevshareOfferID, nil, &sum,
		opts.prefix()+"s2-order-"+stamp, opts.prefix()+"s2-click-"+stamp)
	if err != nil {
		return err
	}
	if err := checkAgentLeg(got.card, agent, self.AgentBase, percent); err != nil {
		return err
	}
	leg := got.card.AgentLeg
	t.Logf("сценарий договора 2 пройден: нога рекрутёра %s ₽ = %s %% от %s (%s)",
		leg.AmountRUB, percent, leg.Base, legBaseAmount(got.card, leg.Base))
	return nil
}

// ── сценарий 3: целевое действие ─────────────────────────────────────────────

func scenarioGoal(ctx context.Context, t TB, c *cpa.Client, opts ConformanceOptions, stamp string) error {
	s := opts.Scenarios
	if s.Goal == "" {
		return errors.New("не задана цель (Goal)")
	}
	offer, _, err := c.Offers.GetForPartner(ctx, s.GoalOfferID)
	if err != nil {
		return fmt.Errorf("оффер %s: %w", s.GoalOfferID, err)
	}
	pay, rev, err := goalRates(ctx, c, s.GoalOfferID, s.Goal)
	if err != nil {
		return err
	}
	sum := s.sum()
	// ⚠ Ставка цели обязана ОТЛИЧАТЬСЯ от ставки оффера — иначе «применена
	// ставка цели» неотличимо от «применена ставка оффера», и сценарий был бы
	// зелёным по неправильной причине.
	wantGoal, err := expectedRUB(pay, sum)
	if err != nil {
		return err
	}
	if wantOffer, err := expectedRUB(offer.PayoutRate, sum); err == nil && wantOffer == wantGoal {
		return fmt.Errorf("ставка цели %s совпадает со ставкой оффера (%s ₽) — сценарий не различает их; нужен оффер, где они разные",
			s.Goal, wantGoal)
	}

	partner := cpa.PartnerExternalID(opts.prefix() + "s3-webmaster")
	if err := upsertWebmaster(ctx, t, c, "С3", partner); err != nil {
		return err
	}
	got, err := convert(ctx, t, c, "С3", partner, s.GoalOfferID, &s.Goal, &sum,
		opts.prefix()+"s3-order-"+stamp, opts.prefix()+"s3-click-"+stamp)
	if err != nil {
		return err
	}
	if got.card.Goal != s.Goal {
		return fmt.Errorf("конверсия записана на цель %q, отправляли %q", got.card.Goal, s.Goal)
	}
	if err := checkAmounts(got.card, pay, rev, sum); err != nil {
		return err
	}
	if err := checkHold(got.card, goalHoldDays(offer, s.Goal), opts.now()); err != nil {
		return err
	}
	t.Logf("сценарий договора 3 пройден: цель %s, выплата %s ₽ по ставке цели, холд до %s. "+
		"⚠ Смену статуса по истечении холда и уведомление набор в том же прогоне не измеряет — "+
		"их доказывает приёмник вебхуков после холда", got.card.Goal, got.card.PayoutRUB,
		got.card.HoldUntil.Format(time.RFC3339))
	return nil
}

// ── сценарий 4: revshare ─────────────────────────────────────────────────────

func scenarioRevshare(ctx context.Context, t TB, c *cpa.Client, opts ConformanceOptions, stamp string) error {
	s := opts.Scenarios
	partnerView, _, err := c.Offers.GetForPartner(ctx, s.RevshareOfferID)
	if err != nil {
		return fmt.Errorf("оффер %s: %w", s.RevshareOfferID, err)
	}
	advertiserView, _, err := c.Offers.GetForAdvertiser(ctx, s.RevshareOfferID)
	if err != nil {
		return fmt.Errorf("оффер %s глазами рекламодателя: %w", s.RevshareOfferID, err)
	}
	if partnerView.PayoutRate.Kind != cpa.RatePercent || advertiserView.RevenueRate.Kind != cpa.RatePercent {
		return fmt.Errorf("оффер %s не revshare: ставки не процентные", s.RevshareOfferID)
	}
	partner := cpa.PartnerExternalID(opts.prefix() + "s4-webmaster")
	if err := upsertWebmaster(ctx, t, c, "С4", partner); err != nil {
		return err
	}
	sum := s.sum()
	got, err := convert(ctx, t, c, "С4", partner, s.RevshareOfferID, nil, &sum,
		opts.prefix()+"s4-order-"+stamp, opts.prefix()+"s4-click-"+stamp)
	if err != nil {
		return err
	}
	if err := checkAmounts(got.card, partnerView.PayoutRate, advertiserView.RevenueRate, sum); err != nil {
		return err
	}
	t.Logf("сценарий договора 4 пройден: сумма заказа %s ₽ → выручка %s (%s %%), партнёру %s (%s %%), оператору %s",
		sum, got.card.RevenueRUB, *advertiserView.RevenueRate.ValuePercent,
		got.card.PayoutRUB, *partnerView.PayoutRate.ValuePercent, got.card.MarginRUB)
	return nil
}

// ── чистые проверки (их доказывают юниты и мутации) ─────────────────────────

// expectedRUB — сколько по ставке причитается с суммы заказа. Процентная ставка
// обязана давать ЦЕЛЫЕ рубли: округление — политика двери, и набор, угадывающий
// его, был бы зелёным или красным по чужой причине. Нецелое — ошибка выбора
// суммы, а не расчёта.
func expectedRUB(r cpa.Rate, sum cpa.RUB) (cpa.RUB, error) {
	switch r.Kind {
	case cpa.RateFixed:
		if r.ValueRUB == nil {
			return 0, errors.New("фиксированная ставка без value_rub")
		}
		return *r.ValueRUB, nil
	case cpa.RatePercent:
		if r.ValuePercent == nil {
			return 0, errors.New("процентная ставка без value_percent")
		}
		return percentOf(sum, string(*r.ValuePercent))
	default:
		return 0, fmt.Errorf("вид ставки %q вне словаря", r.Kind)
	}
}

func percentOf(base cpa.RUB, percent string) (cpa.RUB, error) {
	p, ok := new(big.Rat).SetString(percent)
	if !ok {
		return 0, fmt.Errorf("процент %q не число", percent)
	}
	v := new(big.Rat).Mul(new(big.Rat).SetInt64(int64(base)), p)
	v.Quo(v, big.NewRat(100, 1))
	if !v.IsInt() {
		return 0, fmt.Errorf("%s %% от %s ₽ не целое число рублей — выберите сумму, кратную доле", percent, base)
	}
	return cpa.RUB(v.Num().Int64()), nil
}

func checkTriple(revenue, payout, margin cpa.RUB) error {
	if payout+margin != revenue {
		return fmt.Errorf("тройка ставок не сходится: выплата %s + маржа %s ≠ выручка %s", payout, margin, revenue)
	}
	return nil
}

// checkAmounts — выплата и выручка карточки равны тем, что следуют из ставок.
func checkAmounts(card *cpa.ConversionDetail, pay, rev cpa.Rate, sum cpa.RUB) error {
	wantPay, err := expectedRUB(pay, sum)
	if err != nil {
		return fmt.Errorf("ставка партнёра: %w", err)
	}
	wantRev, err := expectedRUB(rev, sum)
	if err != nil {
		return fmt.Errorf("ставка рекламодателя: %w", err)
	}
	if card.PayoutRUB != wantPay {
		return fmt.Errorf("выплата партнёру %s ₽, по ставке причитается %s ₽", card.PayoutRUB, wantPay)
	}
	if card.RevenueRUB != wantRev {
		return fmt.Errorf("выручка %s ₽, по ставке рекламодателя причитается %s ₽", card.RevenueRUB, wantRev)
	}
	return checkTriple(card.RevenueRUB, card.PayoutRUB, card.MarginRUB)
}

func legBaseAmount(card *cpa.ConversionDetail, base cpa.AgentLegBase) cpa.RUB {
	if base == cpa.AgentLegFromPayout {
		return card.PayoutRUB
	}
	return card.MarginRUB
}

// checkAgentLeg — нога есть, названа база сети, сумма равна проценту от базы.
func checkAgentLeg(card *cpa.ConversionDetail, agent cpa.PartnerExternalID, networkBase cpa.AgentLegBase, percent json.Number) error {
	leg := card.AgentLeg
	if leg == nil {
		return errors.New("в карточке нет ноги рекрутёра (agent_leg = null)")
	}
	if leg.ExternalID != agent {
		return fmt.Errorf("нога рекрутёра начислена %s, ждали %s", leg.ExternalID, agent)
	}
	if leg.Base != networkBase {
		return fmt.Errorf("база ноги %q, а сеть объявляет %q", leg.Base, networkBase)
	}
	if leg.Percent == nil || leg.Percent.String() != percent.String() {
		return fmt.Errorf("процент ноги %v, заводили %s", ptrString(leg.Percent), percent)
	}
	want, err := percentOf(legBaseAmount(card, leg.Base), percent.String())
	if err != nil {
		return err
	}
	if leg.AmountRUB != want {
		return fmt.Errorf("нога рекрутёра %s ₽, а %s %% от %s ₽ — %s ₽",
			leg.AmountRUB, percent, legBaseAmount(card, leg.Base), want)
	}
	return nil
}

// checkHold — срок удержания указан и соответствует холду цели (±1 мин на
// расхождение часов).
func checkHold(card *cpa.ConversionDetail, holdDays int, now time.Time) error {
	if card.HoldUntil == nil {
		return errors.New("срок удержания не указан (hold_until = null)")
	}
	if holdDays <= 0 {
		return nil
	}
	want := card.CreatedAt.Add(time.Duration(holdDays) * 24 * time.Hour)
	if d := card.HoldUntil.Sub(want); d > time.Minute || d < -time.Minute {
		return fmt.Errorf("холд до %s, а по условиям цели (%d дн.) — %s",
			card.HoldUntil.Format(time.RFC3339), holdDays, want.Format(time.RFC3339))
	}
	return nil
}

// goalRates — ставки цели в обеих проекциях.
func goalRates(ctx context.Context, c *cpa.Client, offerID, goal string) (pay, rev cpa.Rate, err error) {
	partnerView, _, err := c.Offers.GetForPartner(ctx, offerID)
	if err != nil {
		return pay, rev, fmt.Errorf("оффер %s: %w", offerID, err)
	}
	advertiserView, _, err := c.Offers.GetForAdvertiser(ctx, offerID)
	if err != nil {
		return pay, rev, fmt.Errorf("оффер %s глазами рекламодателя: %w", offerID, err)
	}
	for _, g := range partnerView.Goals {
		if g.Code == goal && g.PayoutRate != nil {
			pay = *g.PayoutRate
		}
	}
	for _, g := range advertiserView.Goals {
		if g.Code == goal && g.RevenueRate != nil {
			rev = *g.RevenueRate
		}
	}
	if pay.Kind == "" || rev.Kind == "" {
		return pay, rev, fmt.Errorf("у оффера %s нет платной цели %q", offerID, goal)
	}
	return pay, rev, nil
}

func goalHoldDays(offer *cpa.OfferPartnerView, goal string) int {
	for _, g := range offer.Goals {
		if g.Code == goal && g.HoldDays != nil {
			return *g.HoldDays
		}
	}
	if offer.HoldDays != nil {
		return *offer.HoldDays
	}
	return 0
}

func ptrString[T ~string](p *T) string {
	if p == nil {
		return "null"
	}
	return string(*p)
}
