// Ловушки контракта, на которых ломается наивный клиент, и проверки ДО сети.
//
// Каждый случай здесь — не «метод работает», а «метод не делает того, что
// выглядит правильным».
package cpa_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.recca.ru/cpa"
)

// jsonNumber — короткая форма для лексической записи числа.
func jsonNumber(s string) json.Number { return json.Number(s) }

// ⚠⚠ Trafficback — УСПЕХ, а не отказ. Переход принят и записан, но к
// рекламодателю не отправлен: исчерпан кап либо не подошло гео. Код ответа
// успешный, click_id и redirect_url при этом законно null. Клиент, считающий
// успехом только ClickOpen, принял бы штатный ответ двери за поломку; клиент,
// разыменовавший click_id без проверки, упал бы.
func TestTrafficbackIsSuccessWithNilIdentifiers(t *testing.T) {
	const data = `{"click_id":null,"status":"trafficback","reason":"cap",
		"redirect_url":null,"expires_at":null,"created_at":"2026-09-21T00:00:00.000Z",
		"offer_id":"off_1","partner_external_id":"blogger-42"}`

	stand := &apiStand{steps: []apiStep{{status: http.StatusCreated, body: okEnvelopeOf(data)}}}
	click, resp, err := stand.client(t).Clicks.Create(context.Background(), cpa.CreateClickParams{
		PartnerExternalID: "blogger-42", OfferID: "off_1",
	})
	if err != nil {
		t.Fatalf("отбитый переход принят за ошибку: %v", err)
	}
	if click.Status != cpa.ClickTrafficback {
		t.Errorf("статус %q", click.Status)
	}
	if click.ClickID != nil {
		t.Error("click_id обязан остаться nil: конверсию такой переход породить не может")
	}
	if click.RedirectURL != nil {
		t.Error("redirect_url обязан остаться nil: вести некуда, показывайте свою страницу")
	}
	if click.Reason == nil || *click.Reason != "cap" {
		t.Errorf("reason потерян: %v", click.Reason)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("статус ответа %d", resp.StatusCode)
	}
}

// ⚠⚠ Успех — и 201, и 200. Второй приходит на идемпотентном повторе, то есть
// ровно тогда, когда идемпотентность и нужна: после сетевого сбоя. Клиент,
// считающий успехом только 201, сломался бы на первом же таком повторе.
func TestIdempotentRepeatReturns200AndIsStillSuccess(t *testing.T) {
	cases := []struct {
		name   string
		data   string
		invoke func(context.Context, *cpa.Client) error
	}{
		{
			name: "партнёр", data: partnerData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
					ExternalID: "blogger-42", Role: cpa.PartnerWebmaster,
				})
				return err
			},
		},
		{
			name: "ссылка", data: linkData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Links.Create(ctx, cpa.CreateLinkParams{
					PartnerExternalID: "blogger-42", OfferID: "off_1",
				})
				return err
			},
		},
		{
			name: "заявка на выплату", data: payoutData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Payouts.Create(ctx, mustPayout(t))
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, status := range []int{http.StatusCreated, http.StatusOK} {
				stand := &apiStand{steps: []apiStep{{status: status, body: okEnvelopeOf(tc.data)}}}
				if err := tc.invoke(context.Background(), stand.client(t)); err != nil {
					t.Errorf("HTTP %d принят за отказ: %v", status, err)
				}
			}
		})
	}
}

// ⚠⚠ Повтор ДЕНЕЖНОЙ операции с другими параметрами обязан быть отказом, а не
// успехом: успех сообщил бы о деньгах, которых в кошельке нет. Ключ выплаты
// неизменен и включает окно — это дословно находка F-45 реестра Recca.
func TestPayoutMismatchSurfacesAsSentinel(t *testing.T) {
	const conflict = `{"success":false,
		"error":{"code":"IDEMPOTENT_MISMATCH","message":"заявка с этим ключом уже создана на другую сумму"},
		"meta":{"request_id":"req_409"}}`

	stand := &apiStand{steps: []apiStep{{status: http.StatusConflict, body: conflict}}}
	_, resp, err := stand.client(t).Payouts.Create(context.Background(), mustPayout(t))

	if !errors.Is(err, cpa.ErrIdempotentMismatch) {
		t.Fatalf("ошибка = %v, ждали ErrIdempotentMismatch", err)
	}
	var apiErr *cpa.APIError
	if !errors.As(err, &apiErr) || apiErr.RequestID != "req_409" {
		t.Errorf("отказ потерял request_id: %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Errorf("ответ потерян: %+v", resp)
	}
	// ⚠ 409 — это ОТВЕТ, а не сбой: повторять его нечего.
	if got := stand.attempts.Load(); got != 1 {
		t.Errorf("попыток %d, ждали 1", got)
	}
}

// ⚠⚠ Потерянный ответ установки вебхука обязан стать ОШИБКОЙ, а не успехом без
// секрета. Стенд ведёт себя как дверь: на первой попытке ЗАПИСЫВАЕТ секрет и
// роняет её повторяемым кодом (ответ потерялся), а на следующей показывает то же
// состояние уже без секрета — он есть и второй раз не выдаётся
// (`!hadSecret || rotate` в api/federation/v1/cpa/self/webhook/route.ts).
//
// Клиент, повторивший вызов, вернул бы успех с Secret == nil: секрет потерян
// молча, и узнать его больше нечем. Проверяется с ротацией и без неё — секрет
// показывается и при первой установке, не только при ротации.
func TestWebhookLostResponseIsAnErrorNotSilentSuccess(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		t.Run(fmt.Sprintf("rotate=%v", rotate), func(t *testing.T) {
			var (
				mu        sync.Mutex
				hadSecret bool
				attempts  int
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				attempts++
				w.Header().Set("Content-Type", "application/json")
				if !hadSecret {
					hadSecret = true // состояние записано...
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, failEnvelope) // ...а ответ потерян
					return
				}
				_, _ = io.WriteString(w, okEnvelopeOf(
					`{"url":"https://hook.example/cpa","events":["cpa.conversion.approved"],"secret":null}`))
			}))
			t.Cleanup(srv.Close)

			// Клиент С повторами — ровно такой, каким его соберёт интегратор.
			c, err := cpa.New("tk_test_key", &cpa.ClientOptions{BaseURL: srv.URL, MaxRetries: cpa.Ptr(2)})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			config, _, err := c.Self.SetWebhook(context.Background(), cpa.SetWebhookParams{
				URL: "https://hook.example/cpa", RotateSecret: rotate,
			})
			if err == nil {
				t.Fatalf("потерянный ответ стал успехом, секрет = %v — он потерян молча", config.Secret)
			}
			mu.Lock()
			defer mu.Unlock()
			if attempts != 1 {
				t.Errorf("попыток %d, ждали одну: метод не повторяется никогда", attempts)
			}
		})
	}
}

// Секрет из ответа доезжает до вызывающего: показывается он один раз, и потерять
// его значит остаться без проверки подписи событий навсегда.
func TestWebhookSecretReachesCaller(t *testing.T) {
	stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf(webhookData)}}}
	config, _, err := stand.client(t).Self.SetWebhook(context.Background(), cpa.SetWebhookParams{
		URL: "https://hook.example/cpa",
	})
	if err != nil {
		t.Fatalf("SetWebhook: %v", err)
	}
	if config.Secret == nil || *config.Secret != "whsec_shown_once" {
		t.Fatalf("секрет потерян: %v", config.Secret)
	}
	if len(config.Events) != 2 {
		t.Errorf("события потеряны: %v", config.Events)
	}
}

// Клиент передаёт цель КАК ЕСТЬ и не решает за дверь: nil — без ключа, пустая
// строка — ключ с пустым значением. ⚠ Дверь сегодня приводит оба случая к цели
// по умолчанию "1" (`goal || DEFAULT_GOAL`, utils/cpa-api-conversions.ts в
// Recca), то есть различие на проводе адреса не меняет; сворачивать его в SDK
// значило бы принять решение двери второй копией.
func TestConversionGoalHasThreeStatesOnRead(t *testing.T) {
	cases := []struct {
		name      string
		goal      *string
		wantQuery string
	}{
		{"цель не указана — ключа нет", nil, ""},
		{"цель пустая — ключ есть и пуст", cpa.Ptr(""), "goal="},
		{"цель названа", cpa.Ptr("2"), "goal=2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf(conversionDetailData)}}}
			_, _, err := stand.client(t).Conversions.Get(context.Background(), cpa.GetConversionParams{
				ExternalID: "order-1", Goal: tc.goal,
			})
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if stand.queries[0] != tc.wantQuery {
				t.Errorf("строка запроса %q, ждали %q", stand.queries[0], tc.wantQuery)
			}
		})
	}
}

// ⚠⚠ Граница проекции держится ТИПОМ, а не дисциплиной вызывающего. Партнёрская
// проекция не несёт ставки рекламодателя и маржи ни одним полем: будь они в
// структуре, первый же метод, случайно запросивший view=advertiser, заполнил бы
// их — и блогер увидел бы маржу оператора.
func TestPartnerProjectionCannotCarryAdvertiserRates(t *testing.T) {
	t.Parallel()

	forbidden := map[string]bool{"revenue_rate": true, "margin_rub": true, "target_cpl_rub": true}
	checked := 0

	var walk func(reflect.Type, string)
	walk = func(t2 reflect.Type, path string) {
		for t2.Kind() == reflect.Pointer || t2.Kind() == reflect.Slice {
			t2 = t2.Elem()
		}
		if t2.Kind() != reflect.Struct {
			return
		}
		for i := range t2.NumField() {
			field := t2.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			checked++
			if forbidden[name] {
				t.Errorf("%s.%s несёт %s — партнёрская проекция показала бы маржу оператора", path, field.Name, name)
			}
			if field.Type != reflect.TypeOf(time.Time{}) {
				walk(field.Type, path+"."+field.Name)
			}
		}
	}
	walk(reflect.TypeOf(cpa.OfferPartnerView{}), "OfferPartnerView")

	if checked == 0 {
		t.Fatal("не проверено ни одного поля — обход типа сломан")
	}
	// Вторая половина: запрет доказывает разрешение. У проекции рекламодателя
	// собственная ставка БЫТЬ обязана, иначе тест был бы зелен на пустом типе.
	if _, ok := reflect.TypeOf(cpa.OfferAdvertiserView{}).FieldByName("RevenueRate"); !ok {
		t.Error("у проекции рекламодателя нет собственной ставки — проверка выше зелена по пустоте")
	}
}

// Проверки ДО сети: клиент отказывает сам там, где прав наверняка, и называет
// поле. Отказ несёт ErrValidation — отличать его от ответа двери необходимо,
// иначе интегратор будет искать свой request_id в чужих журналах.
func TestValidationBeforeNetwork(t *testing.T) {
	cases := []struct {
		name      string
		invoke    func(context.Context, *cpa.Client) error
		wantField string
	}{
		{
			name: "партнёр: знак вне алфавита",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
					ExternalID: "блогер", Role: cpa.PartnerWebmaster,
				})
				return err
			},
			wantField: "partner_external_id",
		},
		{
			name: "партнёр: рекламодателя машинная дверь не заводит",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
					ExternalID: "adv-1", Role: cpa.PartnerAdvertiser,
				})
				return err
			},
			wantField: "role",
		},
		{
			name: "партнёр: ставка агента вне диапазона",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				percent := jsonNumber("120")
				_, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
					ExternalID: "agent-1", Role: cpa.PartnerAgent, AgentPercent: &percent,
				})
				return err
			},
			wantField: "agent_percent",
		},
		{
			name: "конверсия: зарезервированный префикс",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
					ClickID: "clk_1", ExternalID: "recca:internal-1", Status: "approved",
				})
				return err
			},
			wantField: "external_id",
		},
		{
			name: "конверсия: сумма выше потолка контракта",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
					ClickID: "clk_1", ExternalID: "order-1", Status: "approved",
					SumRUB: cpa.RUBPtr(cpa.MaxSumRUB + 1),
				})
				return err
			},
			wantField: "sum_rub",
		},
		{
			name: "клик: трёхбуквенный код страны",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Clicks.Create(ctx, cpa.CreateClickParams{
					PartnerExternalID: "blogger-42", OfferID: "off_1", Country: "RUS",
				})
				return err
			},
			wantField: "country",
		},
		{
			name: "ссылка: оффер не назван",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Links.Create(ctx, cpa.CreateLinkParams{PartnerExternalID: "blogger-42"})
				return err
			},
			wantField: "offer_id",
		},
		{
			// ⚠ Дверь без advertiser_id при view=advertiser отвечает 400 —
			// значит отказать обязан клиент, и до сети.
			name: "проекция рекламодателя: рекламодатель не назван",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Offers.ListForAdvertiser(ctx, "", cpa.ListOffersParams{})
				return err
			},
			wantField: "advertiser_id",
		},
		{
			name: "проекция рекламодателя: рекламодатель задан дважды по-разному",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Offers.ListForAdvertiser(ctx, "adv_1",
					cpa.ListOffersParams{AdvertiserID: "adv_2"})
				return err
			},
			wantField: "advertiser_id",
		},
		{
			name: "оффер: идентификатор пуст",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Offers.GetForPartner(ctx, "")
				return err
			},
			wantField: "offer_id",
		},
		{
			name: "вебхук: адрес без схемы",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Self.SetWebhook(ctx, cpa.SetWebhookParams{URL: "hook.example/cpa"})
				return err
			},
			wantField: "url",
		},
		{
			name: "отчёт: разрез не назван",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Statistics.Get(ctx, cpa.StatisticsParams{})
				return err
			},
			wantField: "group_by",
		},
		{
			name: "страница: отрицательный размер",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Partners.List(ctx, cpa.ListPartnersParams{
					PageParams: cpa.PageParams{Limit: -1},
				})
				return err
			},
			wantField: "limit",
		},
		{
			name: "выплата: нулевое значение параметров в дверь не проходит",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				var empty cpa.PayoutParams
				_, _, err := c.Payouts.Create(ctx, empty)
				return err
			},
			wantField: "partner_external_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ⚠ Стенд заведомо отвечает отказом: дойди запрос до двери — тест
			// покраснел бы, и «проверка до сети» была бы неотличима от «дверь
			// отбила». Здесь проверяется, что запроса НЕ БЫЛО.
			stand := &apiStand{steps: []apiStep{{status: http.StatusInternalServerError, body: failEnvelope}}}
			err := tc.invoke(context.Background(), stand.client(t))

			if err == nil {
				t.Fatal("негодные параметры приняты")
			}
			if !errors.Is(err, cpa.ErrValidation) {
				t.Errorf("ошибка = %v, ждали ErrValidation", err)
			}
			var validationErr *cpa.ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("ошибка не называет поле: %v", err)
			}
			if validationErr.Field != tc.wantField {
				t.Errorf("поле %q, ждали %q", validationErr.Field, tc.wantField)
			}
			if got := stand.attempts.Load(); got != 0 {
				t.Errorf("запросов к двери %d — проверка обязана была отказать ДО сети", got)
			}
		})
	}
}

// ⚠⚠ Зарезервированный префикс проверяется ПРАВИЛОМ ДВЕРИ: без регистра и после
// обрезки пробелов (`externalId.trim().toLowerCase().startsWith("recca:")`,
// utils/conversion-external-id.ts в Recca). Иначе `RECCA:x` уходил бы в сеть и
// отказ приходил от двери, а проверка до сети делала бы вид, что держит границу.
func TestReservedPrefixFollowsDoorRule(t *testing.T) {
	create := func(externalID string) func(context.Context, *cpa.Client) error {
		return func(ctx context.Context, c *cpa.Client) error {
			_, _, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
				ClickID: "clk_1", ExternalID: externalID, Status: "approved",
			})
			return err
		}
	}

	refused := []string{"recca:x", "RECCA:x", "Recca:x", " recca:x", "\trecca:x", "  RECCA:order"}
	for _, id := range refused {
		t.Run("отказ "+strconv.Quote(id), func(t *testing.T) {
			stand := &apiStand{steps: []apiStep{{status: http.StatusCreated, body: okEnvelopeOf(conversionDetailData)}}}
			err := create(id)(context.Background(), stand.client(t))

			var validationErr *cpa.ValidationError
			if !errors.As(err, &validationErr) || validationErr.Field != "external_id" {
				t.Fatalf("ошибка %v, ждали отказ до сети по external_id", err)
			}
			if got := stand.attempts.Load(); got != 0 {
				t.Errorf("запрос ушёл в дверь (%d) — проверка до сети пропустила префикс", got)
			}
			if !cpa.IsReservedExternalID(id) {
				t.Error("IsReservedExternalID расходится с проверкой метода")
			}
		})
	}

	// Запрет доказывает разрешение: «recca:» НЕ в начале — обычный номер.
	for _, id := range []string{"my-recca:x", "reccax", "order-recca:1"} {
		t.Run("пропуск "+strconv.Quote(id), func(t *testing.T) {
			stand := &apiStand{steps: []apiStep{{status: http.StatusCreated, body: okEnvelopeOf(conversionDetailData)}}}
			if err := create(id)(context.Background(), stand.client(t)); err != nil {
				t.Errorf("обычный номер отбит: %v", err)
			}
		})
	}
}

// ⚠ Двери чтения и отмены префикс НЕ проверяют (замер 23.09) — значит и клиент
// до сети не отказывает: отказ обещал бы то, чего дверь не утверждает.
func TestReservedPrefixIsNotRefusedOnReadAndCancel(t *testing.T) {
	cases := []struct {
		name   string
		invoke func(context.Context, *cpa.Client) error
	}{
		{"чтение", func(ctx context.Context, c *cpa.Client) error {
			_, _, err := c.Conversions.Get(ctx, cpa.GetConversionParams{ExternalID: "recca:internal-1"})
			return err
		}},
		{"отмена", func(ctx context.Context, c *cpa.Client) error {
			_, _, err := c.Conversions.Cancel(ctx, cpa.CancelConversionParams{ExternalID: "recca:internal-1"})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf(conversionDetailData)}}}
			if err := tc.invoke(context.Background(), stand.client(t)); err != nil {
				t.Fatalf("клиент отказал сам: %v", err)
			}
			if got := stand.attempts.Load(); got != 1 {
				t.Errorf("запросов %d, ждали один — решение за дверью", got)
			}
		})
	}
}

// ⚠⚠ Заявка на выплату без окна не собирается ВОВСЕ: окно входит в ключ
// идемпотентности, и заявка без него попала бы в ту же строку, что заявка другого
// месяца. Конструктор — единственный вход, и он обязан отказывать.
func TestPayoutParamsRefuseIncompleteWindow(t *testing.T) {
	sept := cpa.NewDate(2026, time.September, 1)
	oct := cpa.NewDate(2026, time.October, 1)

	cases := []struct {
		name      string
		partner   cpa.PartnerExternalID
		amount    cpa.RUB
		from, to  cpa.Date
		wantField string
	}{
		{"без начала окна", "blogger-42", 1500, cpa.Date{}, oct, "period_from"},
		{"без конца окна", "blogger-42", 1500, sept, cpa.Date{}, "period_to"},
		{"конец раньше начала", "blogger-42", 1500, oct, sept, "period_to"},
		{"нулевая сумма", "blogger-42", 0, sept, oct, "amount_rub"},
		{"отрицательная сумма", "blogger-42", -1, sept, oct, "amount_rub"},
		{"партнёр не назван", "", 1500, sept, oct, "partner_external_id"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpa.NewPayoutParams(tc.partner, tc.amount, tc.from, tc.to)
			if err == nil {
				t.Fatal("неполная заявка собралась — окно входит в ключ идемпотентности")
			}
			var validationErr *cpa.ValidationError
			if !errors.As(err, &validationErr) || validationErr.Field != tc.wantField {
				t.Errorf("ошибка %v, ждали поле %q", err, tc.wantField)
			}
		})
	}

	// Вторая половина инварианта: полная заявка обязана собираться, иначе
	// конструктор был бы «всегда красным» и доказывал бы не то.
	if _, err := cpa.NewPayoutParams("blogger-42", 1500, sept, oct); err != nil {
		t.Errorf("полная заявка отвергнута: %v", err)
	}
	// ⚠ Потолок MaxSumRUB — конверсии, а не выплаты: у двери выплат он не
	// объявлен, и клиент не вправе отказывать выше него.
	if _, err := cpa.NewPayoutParams("blogger-42", cpa.MaxSumRUB+1, sept, oct); err != nil {
		t.Errorf("заявка выше потолка КОНВЕРСИИ отвергнута — у выплат такого потолка нет: %v", err)
	}
}
