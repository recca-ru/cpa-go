package cpatest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.recca.ru/cpa"
	"go.recca.ru/cpa/postback"
	"go.recca.ru/cpa/webhook"
)

// TB — то, что Conformance требует от теста. Интерфейс, а не *testing.T, по одной
// причине: иначе «а краснеет ли этот набор вообще» нельзя было бы проверить
// тестом, и он остался бы утверждением о себе самом.
//
// *testing.T этому интерфейсу удовлетворяет.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
}

// ConformanceOptions — что подкрутить под конкретную площадку.
type ConformanceOptions struct {
	// SkipPayouts пропускает цепочку выплат целиком.
	//
	// Нужен теперь редко. Партнёр без одобренного баланса цепочку НЕ роняет: набор
	// читает can_request и при false проверяет, что дверь отказывает
	// INSUFFICIENT_BALANCE, — это согласованность баланса и заявок, и она зелёная.
	// Положительная ветка (заявка и отказ на другой сумме) тогда не измерена, и
	// набор пишет об этом в журнал теста. Пропускать цепочку стоит, только когда
	// площадке нельзя создавать заявки вовсе.
	SkipPayouts bool
	// PartnerExternalID — под каким партнёром идти. Пусто — conformance-webmaster.
	PartnerExternalID cpa.PartnerExternalID
	// OfferID — на каком оффере. Пусто — первый из каталога.
	OfferID string
	// ExternalIDPrefix — префикс номеров заказа, которые набор создаёт.
	//
	// ⚠ Пространство номеров общее для СЕТИ: без своего префикса прогон набора
	// столкнулся бы с живыми заказами интегратора и получил 409
	// EXTERNAL_ID_CONFLICT.
	ExternalIDPrefix string
	// Now — «сейчас» для проверок подписи; пусто — time.Now.
	Now time.Time
}

func (o ConformanceOptions) partner() cpa.PartnerExternalID {
	if o.PartnerExternalID != "" {
		return o.PartnerExternalID
	}
	return "conformance-webmaster"
}

func (o ConformanceOptions) prefix() string {
	if o.ExternalIDPrefix != "" {
		return o.ExternalIDPrefix
	}
	return "conformance-"
}

func (o ConformanceOptions) now() time.Time {
	if !o.Now.IsZero() {
		return o.Now
	}
	return time.Now()
}

// Conformance прогоняет приёмочные сценарии против клиента — против стенда
// (структура) или против живой песочницы (поведение).
//
// ⚠⚠ Зелёный прогон против стенда НЕ означает готовности к бою: стенд отвечает
// фикстурами, и доказывает он только то, что ваш код собирает верные запросы и
// разбирает верные ответы. Тройка ставок, срок холда, ключи идемпотентности и
// право партнёра работать — предмет песочницы.
//
// Цепочки независимы: падение одной не прячет остальные. Fatalf набор не зовёт
// нигде намеренно — он сообщает обо ВСЁМ, что нашёл, а не о первом найденном.
//
// # Набор работает ТОЛЬКО против песочницы и стенда
//
// ⚠⚠ До первого запроса набор сверяет базовый адрес клиента. Разрешены два:
// адрес песочницы и петля (127.0.0.1, localhost, [::1] — там живёт
// cpatest.NewServer). На любом другом адресе — отказ и НИ ОДНОГО запроса.
//
// Довод — деньги. Набор создаёт партнёра, одобренную конверсию на 2500 ₽ (её
// выручка выставляется рекламодателю) и заявку на выплату. Живёт он в `go test
// ./...` интегратора, то есть в его CI, а нулевое значение Environment — это
// Production. Забытое `Environment: cpa.Sandbox` при боевом ключе превратило бы
// каждый прогон CI в настоящие деньги в живой сети. Ключи сред неотличимы,
// поэтому ремней два: до первого запроса решает адрес, а после `GET /cpa/self`
// — среда, которую называет сам сервер (`environment`). Любая среда, кроме
// `sandbox`, останавливает набор до первой записи.
//
// Флага «разрешить прод» нет и не будет: набор, который можно направить на
// живую сеть одной опцией, туда однажды и направят.
func Conformance(t TB, c *cpa.Client, opts ConformanceOptions) {
	t.Helper()

	if err := checkTarget(c); err != nil {
		t.Errorf("Conformance отказывается работать: %v", err)
		return
	}

	ctx := context.Background()

	env, err := chainSelf(ctx, t, c)
	if err != nil {
		t.Errorf("сценарий 1 (сведения о сети): %v", err)
	}
	// Второй ремень поверх адреса: среду называет СЕРВЕР. Адрес песочницы можно
	// направить не туда (прокси, DNS, петля, проброшенная на живую машину), ответ
	// двери о себе — нет. Всё ниже пишет данные, поэтому без подтверждённой
	// песочницы не исполняется ни одна цепочка.
	if env != cpa.DeploymentSandbox {
		t.Errorf("Conformance остановлен до первой записи: сервер называет среду %q, "+
			"а набор создаёт партнёра, конверсию на 2500 ₽ и заявку на выплату — это допустимо "+
			"только в песочнице (environment = sandbox)", env)
		// Подписи сети не касаются — их проверить можно всегда.
		if err := checkSignatures(opts.now()); err != nil {
			t.Errorf("круговая проверка подписей: %v", err)
		}
		return
	}

	offerID, err := chainCatalogue(ctx, t, c, opts)
	if err != nil {
		t.Errorf("сценарий 2 (каталог и материалы): %v", err)
	}

	if offerID == "" {
		t.Errorf("сценарий 3 (вертикаль) пропущен: оффера для прогона не нашлось")
	} else if err := chainVertical(ctx, t, c, opts, offerID); err != nil {
		t.Errorf("сценарий 3 (ссылка → клик → конверсия): %v", err)
	}

	switch {
	case opts.SkipPayouts:
		t.Logf("сценарий 4 (баланс и выплата) пропущен по SkipPayouts")
	default:
		if err := chainPayout(ctx, t, c, opts); err != nil {
			t.Errorf("сценарий 4 (баланс и выплата): %v", err)
		}
	}

	// Подписи проверяются всегда: сети в них нет, и от площадки они не зависят.
	if err := checkSignatures(opts.now()); err != nil {
		t.Errorf("круговая проверка подписей: %v", err)
	}
}

// ── куда направлен набор ─────────────────────────────────────────────────────

// loopbackHosts — где живёт стенд cpatest.NewServer.
var loopbackHosts = map[string]bool{"127.0.0.1": true, "localhost": true, "::1": true}

// checkTarget решает, можно ли гнать набор против этого клиента. Запросов он не
// делает.
func checkTarget(c *cpa.Client) error {
	target, err := url.Parse(c.BaseURL())
	if err != nil {
		return fmt.Errorf("базовый адрес клиента %q не разбирается: %w", c.BaseURL(), err)
	}
	if loopbackHosts[strings.ToLower(target.Hostname())] {
		return nil
	}

	// Адрес песочницы — у самого клиента, а не второй копией здесь: разойдись
	// они, набор отказывал бы песочнице или пускал бы не туда.
	probe, err := cpa.New("conformance-target-probe", &cpa.ClientOptions{Environment: cpa.Sandbox})
	if err != nil {
		return fmt.Errorf("адрес песочницы не определён: %w", err)
	}
	sandbox, err := url.Parse(probe.BaseURL())
	if err != nil {
		return fmt.Errorf("адрес песочницы %q не разбирается: %w", probe.BaseURL(), err)
	}
	if strings.EqualFold(target.Scheme, sandbox.Scheme) &&
		strings.EqualFold(target.Host, sandbox.Host) &&
		strings.Trim(target.Path, "/") == "" {
		return nil
	}

	return fmt.Errorf(
		"клиент смотрит на %s, а набор создаёт партнёра, одобренную конверсию на 2500 ₽ "+
			"и заявку на выплату — в живой сети это настоящие деньги. Разрешены песочница (%s) "+
			"и петля (стенд cpatest.NewServer). Соберите клиента с Environment: cpa.Sandbox; "+
			"ни одного запроса не сделано",
		c.BaseURL(), probe.BaseURL())
}

// ── сценарий 1: сеть отвечает и называет свои условия ────────────────────────

// chainSelf возвращает среду, названную сервером, даже когда остальные проверки
// сценария не прошли: решение «можно ли писать» от них не зависит.
func chainSelf(ctx context.Context, t TB, c *cpa.Client) (cpa.DeploymentEnvironment, error) {
	info, resp, err := c.Self.Get(ctx)
	if err != nil {
		return "", err
	}
	env := info.Environment
	if info.ContractVersion == "" {
		return env, errors.New("contract_version пуста — сверить версию контракта нечем")
	}
	if len(info.Scopes) == 0 {
		return env, errors.New("скоупы пусты: ключ без прав неотличим от ключа, о правах которого не сказали")
	}
	if info.RateLimitPerMin <= 0 {
		return env, fmt.Errorf("rate_limit_per_min = %d: бюджет запросов обязан быть положительным",
			info.RateLimitPerMin)
	}
	if resp.RequestID == "" {
		return env, errors.New("request_id пуст — сослаться в обращении будет не на что")
	}
	t.Logf("сеть %s, среда %s, контракт %s, бюджет %d/мин, база ноги %s",
		info.NetworkID, env, info.ContractVersion, info.RateLimitPerMin, info.AgentBase)
	return env, nil
}

// ── сценарий 2: каталог, оффер, материалы ────────────────────────────────────

func chainCatalogue(ctx context.Context, t TB, c *cpa.Client, opts ConformanceOptions) (string, error) {
	offers, _, err := c.Offers.ListForPartner(ctx, cpa.ListOffersParams{
		PageParams: cpa.PageParams{Limit: 20},
	})
	if err != nil {
		return "", err
	}
	if len(offers) == 0 {
		return "", errors.New("каталог пуст: прогнать вертикаль не на чем")
	}

	offerID := opts.OfferID
	if offerID == "" {
		offerID = offers[0].ID
	}

	offer, _, err := c.Offers.GetForPartner(ctx, offerID)
	if err != nil {
		return offerID, fmt.Errorf("карточка оффера %s: %w", offerID, err)
	}
	// ⚠⚠ Режим гео — не просто список: прочитав deny как allow, интегратор полил
	// бы трафик туда, откуда его отбивают. Значит режим обязан приезжать.
	switch offer.Geo.Mode {
	case cpa.GeoAny, cpa.GeoAllow, cpa.GeoDeny:
	default:
		return offerID, fmt.Errorf("режим гео %q вне словаря контракта", offer.Geo.Mode)
	}
	if offer.PayoutRate.Kind == "" {
		return offerID, errors.New("у ставки оффера нет вида: сколько платят — неизвестно")
	}
	// ⚠ Веха — цель БЕЗ ставки, и ставка у неё обязана быть null. Ноль на её месте
	// читался бы как «платят нуль», то есть как настроенная цель.
	for _, goal := range offer.Goals {
		if goal.IsMilestone && goal.PayoutRate != nil {
			return offerID, fmt.Errorf("у вехи %s есть ставка — за веху не платят", goal.Code)
		}
	}

	creatives, _, err := c.Offers.Creatives(ctx, offerID)
	if err != nil {
		return offerID, fmt.Errorf("материалы оффера %s: %w", offerID, err)
	}
	for _, creative := range creatives {
		// ⚠ У текстового креатива файла нет по построению, а у ленда — есть.
		// Разыменование url без ветвления по kind уронило бы интегратора.
		if creative.Kind == cpa.CreativeText && creative.Text == nil {
			return offerID, fmt.Errorf("у текстового материала %s нет текста", creative.ID)
		}
	}
	t.Logf("оффер %s: ставка %s, целей %d, материалов %d",
		offerID, offer.PayoutRate.Kind, len(offer.Goals), len(creatives))
	return offerID, nil
}

// ── сценарий 3: партнёр → ссылка → клик → конверсия ──────────────────────────

func chainVertical(ctx context.Context, t TB, c *cpa.Client, opts ConformanceOptions, offerID string) error {
	partner := opts.partner()

	if _, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
		ExternalID: partner, Role: cpa.PartnerWebmaster, PartnerState: cpa.PartnerStateActive,
	}); err != nil {
		return fmt.Errorf("партнёр %s: %w", partner, err)
	}

	link, _, err := c.Links.Create(ctx, cpa.CreateLinkParams{
		PartnerExternalID: partner, OfferID: offerID,
	})
	if err != nil {
		return fmt.Errorf("ссылка: %w", err)
	}
	if link.TargetURL == "" {
		return errors.New("ссылка без target_url: вести человека некуда")
	}

	// ⚠ Один штамп прогона на ключ клика и номер заказа. Постоянный ключ клика
	// вернул бы со второго прогона клик ПЕРВОГО (ключ отправителя идемпотентен),
	// и вертикаль проверяла бы меньше, чем заявляет, — а старый клик ещё и
	// упёрся бы в окно атрибуции.
	stamp := strconv.FormatInt(opts.now().Unix(), 10)

	click, _, err := c.Clicks.Create(ctx, cpa.CreateClickParams{
		PartnerExternalID: partner, OfferID: offerID,
		ClientClickID: opts.prefix() + "click-" + stamp, Country: "RU",
	})
	if err != nil {
		return fmt.Errorf("переход: %w", err)
	}
	if click.Status == cpa.ClickTrafficback {
		// Не ошибка: кап исчерпан либо не подошло гео. Но конверсию такой переход
		// породить не может, и продолжать цепочку нечем.
		return fmt.Errorf("переход отбит (%s) — вертикаль дальше не идёт, и это ответ двери, а не дефект SDK",
			valueOr(click.Reason, "без причины"))
	}
	if click.ClickID == nil {
		return errors.New("переход принят, но click_id не выдан — конверсию привязать не к чему")
	}

	externalID := opts.prefix() + "order-" + stamp

	conversion, resp, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
		ClickID: *click.ClickID, ExternalID: externalID, Status: "approved",
		SumRUB: cpa.RUBPtr(2500),
	})
	if err != nil {
		return fmt.Errorf("конверсия: %w", err)
	}

	// ⚠⚠ ИНВАРИАНТ п. 3.4 договора: тройка ставок сходится. Это единственная
	// проверка набора, которая говорит о ДЕНЬГАХ, и расходится она только при
	// настоящей поломке расчёта.
	if conversion.PayoutRUB+conversion.MarginRUB != conversion.RevenueRUB {
		return fmt.Errorf("тройка ставок не сходится: выплата %s + маржа %s ≠ выручка %s (request_id %s)",
			conversion.PayoutRUB, conversion.MarginRUB, conversion.RevenueRUB, resp.RequestID)
	}
	if conversion.ExternalID != externalID {
		return fmt.Errorf("дверь вернула конверсию %s вместо %s", conversion.ExternalID, externalID)
	}

	// ⚠⚠ Идемпотентный повтор обязан вернуть ТУ ЖЕ конверсию, а не вторую.
	repeat, repeatResp, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
		ClickID: *click.ClickID, ExternalID: externalID, Status: "approved",
		SumRUB: cpa.RUBPtr(2500),
	})
	if err != nil {
		return fmt.Errorf("идемпотентный повтор конверсии отбит: %w", err)
	}
	if repeat.ExternalID != conversion.ExternalID {
		return fmt.Errorf("повтор вернул другую конверсию: %s вместо %s",
			repeat.ExternalID, conversion.ExternalID)
	}
	if repeatResp.StatusCode == 201 {
		return errors.New("повтор ответил 201 — значит записал ВТОРУЮ конверсию, а не вернул первую")
	}

	// Карточка читается по той же тройке, которой создавалась.
	card, _, err := c.Conversions.Get(ctx, cpa.GetConversionParams{ExternalID: externalID})
	if err != nil {
		return fmt.Errorf("карточка конверсии: %w", err)
	}
	if card.PayoutRUB+card.MarginRUB != card.RevenueRUB {
		return fmt.Errorf("тройка ставок в карточке не сходится: %s + %s ≠ %s",
			card.PayoutRUB, card.MarginRUB, card.RevenueRUB)
	}

	t.Logf("вертикаль пройдена: %s, выплата %s + маржа %s = выручка %s",
		externalID, conversion.PayoutRUB, conversion.MarginRUB, conversion.RevenueRUB)
	return nil
}

// ── сценарий 4: баланс и выплата ─────────────────────────────────────────────

func chainPayout(ctx context.Context, t TB, c *cpa.Client, opts ConformanceOptions) error {
	partner := opts.partner()

	balance, _, err := c.Partners.Balance(ctx, partner)
	if err != nil {
		return fmt.Errorf("баланс %s: %w", partner, err)
	}
	t.Logf("баланс %s: доступно %s, в холде %s, порог %s, можно заявку: %v",
		partner, balance.AvailableRUB, balance.OnHoldRUB, balance.MinPayoutRUB, balance.CanRequest)

	from := cpa.DateOf(opts.now().AddDate(0, 0, -30))
	to := cpa.DateOf(opts.now())
	amount := balance.MinPayoutRUB
	if amount <= 0 {
		amount = 1
	}

	params, err := cpa.NewPayoutParams(partner, amount, from, to)
	if err != nil {
		return fmt.Errorf("заявка не собралась: %w", err)
	}

	// ⚠⚠ Инвариант — СОГЛАСОВАННОСТЬ баланса и двери заявок, а не «заявка
	// проходит». can_request — ответ двери на вопрос «пройдёт ли заявка прямо
	// сейчас» (Recca выводит его из доступного и порога), и набор проверяет, что
	// дверь заявок отвечает на тот же вопрос так же. Сумма — порог, поэтому
	// законный отказ при can_request == false ровно один: INSUFFICIENT_BALANCE.
	//
	// Идти в заявку вслепую, как делала первая редакция, значило краснеть на
	// КАЖДОЙ свежей песочнице: у нового партнёра всё в холде, и дверь права.
	payout, _, err := c.Payouts.Create(ctx, params)
	if !balance.CanRequest {
		switch {
		case err == nil:
			return fmt.Errorf("баланс врёт: can_request = false (доступно %s, порог %s), а дверь приняла заявку %s",
				balance.AvailableRUB, balance.MinPayoutRUB, payout.ID)
		case !errors.Is(err, cpa.ErrInsufficientBalance):
			return fmt.Errorf("can_request = false, ждали отказ INSUFFICIENT_BALANCE, получили: %w", err)
		}
		t.Logf("положительная ветка выплаты не измерена: доступно %s, порог %s — отказ INSUFFICIENT_BALANCE согласован с балансом",
			balance.AvailableRUB, balance.MinPayoutRUB)
		return nil
	}
	if errors.Is(err, cpa.ErrInsufficientBalance) {
		return fmt.Errorf("can_request врёт: баланс обещал заявку (доступно %s, порог %s), а дверь отказала: %w",
			balance.AvailableRUB, balance.MinPayoutRUB, err)
	}
	if err != nil {
		return fmt.Errorf("заявка: %w", err)
	}
	if payout.AmountRUB != amount {
		return fmt.Errorf("дверь вернула заявку на %s вместо %s", payout.AmountRUB, amount)
	}

	// ⚠⚠ Повтор с ДРУГОЙ суммой обязан быть отказом, а не успехом. Успех сообщил
	// бы о деньгах, которых в кошельке нет — дословно находка F-42 реестра Recca.
	other, err := cpa.NewPayoutParams(partner, amount+1, from, to)
	if err != nil {
		return fmt.Errorf("заявка на другую сумму не собралась: %w", err)
	}
	if _, _, err := c.Payouts.Create(ctx, other); !errors.Is(err, cpa.ErrIdempotentMismatch) {
		return fmt.Errorf("повтор с другой суммой дал %v, ждали IDEMPOTENT_MISMATCH", err)
	}

	if _, _, err := c.Payouts.List(ctx, cpa.ListPayoutsParams{PartnerExternalID: partner}); err != nil {
		return fmt.Errorf("история заявок: %w", err)
	}
	return nil
}

// ── круговая проверка подписей ───────────────────────────────────────────────

// checkSignatures доказывает, что интегратор одинаково СОБИРАЕТ и ПРОВЕРЯЕТ
// подпись, и что подделанное тело проверку не проходит.
//
// ⚠ Векторы платформы проверяют пакеты postback и webhook своими тестами; здесь
// круговая проверка — она не требует ни сети, ни файлов и потому идёт на любой
// площадке.
func checkSignatures(now time.Time) error {
	const secret = "conformance-secret"

	fields := postback.Fields{
		ClickID: "clk_1", ExternalID: "order-1", Status: "approved",
		SumRUB: strPtr("12.5"), SubID: "tg", Goal: strPtr("1"),
		Timestamp: strPtr(strconv.FormatInt(now.Unix(), 10)),
	}
	signature := postback.Sign(fields, secret)
	if !postback.Verify(fields, secret, signature) {
		return errors.New("подпись постбэка не проверяется своей же проверкой")
	}
	// Запрет обязан доказать разрешение: подделка обязана НЕ проходить.
	tampered := fields
	tampered.SumRUB = strPtr("125")
	if postback.Verify(tampered, secret, signature) {
		return errors.New("подпись постбэка сошлась на ПОДМЕНЁННОЙ сумме")
	}
	if !postback.CheckFreshness(now.Unix(), now, 5*time.Minute) {
		return errors.New("свежая метка времени постбэка признана просроченной")
	}

	body := []byte(`{"event_type":"cpa.conversion.approved","event_id":"evt_1"}`)
	header := webhookHeader(secret, body, now)
	if !webhook.Verify(secret, header, body, now, 300*time.Second) {
		return errors.New("подпись вебхука не проверяется своей же проверкой")
	}
	if webhook.Verify(secret, header, append(body, ' '), now, 300*time.Second) {
		return errors.New("подпись вебхука сошлась на ИЗМЕНЁННОМ теле")
	}
	if webhook.Verify(secret, header, body, now.Add(time.Hour), 300*time.Second) {
		return errors.New("просроченная подпись вебхука принята")
	}
	return nil
}

// webhookHeader собирает заголовок так, как его шлёт Recca: t=<unix>,v1=<hex>
// над "<t>.<body>".
func webhookHeader(secret string, body []byte, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func strPtr(s string) *string { return &s }

func valueOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}
