// Контракт формы запроса у КАЖДОГО метода: путь, метод HTTP, строка запроса,
// тело — и политика повтора.
//
// Источник — docs/federation/cpa-api.md (раздел 9) и openapi-cpa.yaml, а не код
// платформы.
//
// ⚠⚠ У таблицы есть ЗНАМЕНАТЕЛЬ: TestRequestTableCoversEveryMethod перечисляет
// методы сервисов рефлексией и роняет прогон, если метод есть, а случая для него
// нет. Без этого «семнадцать методов покрыты» означало бы «покрыто столько,
// сколько я успел вписать», и разница была бы невидима.
package cpa_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.recca.ru/cpa"
)

// ── стенд ────────────────────────────────────────────────────────────────────

type apiStep struct {
	status int
	body   string
}

// apiStand записывает всё, что увидела дверь, и отвечает по сценарию.
type apiStand struct {
	attempts atomic.Int32
	methods  []string
	paths    []string
	queries  []string
	bodies   []string
	steps    []apiStep
}

func (s *apiStand) client(t *testing.T) *cpa.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.attempts.Add(1)) - 1
		raw, _ := io.ReadAll(r.Body)
		s.methods = append(s.methods, r.Method)
		s.paths = append(s.paths, r.URL.EscapedPath())
		s.queries = append(s.queries, r.URL.RawQuery)
		s.bodies = append(s.bodies, string(raw))

		step := s.steps[len(s.steps)-1]
		if n < len(s.steps) {
			step = s.steps[n]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(step.status)
		_, _ = io.WriteString(w, step.body)
	}))
	t.Cleanup(srv.Close)

	c, err := cpa.New("tk_test_key", &cpa.ClientOptions{BaseURL: srv.URL, MaxRetries: cpa.Ptr(1)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func okEnvelopeOf(data string) string {
	return `{"success":true,"data":` + data + `,"meta":{"request_id":"req_1"}}`
}

const failEnvelope = `{"success":false,"error":{"code":"INTERNAL_ERROR","message":"boom"}}`

// ── фикстуры ─────────────────────────────────────────────────────────────────
//
// ⚠ Здесь литералы, и это решение, а не отставание от cpatest (там фикстуры
// файлами — из них собирается СТЕНД, и правка контракта обязана менять один
// файл). Эти же тела проверяют ФОРМУ, и литерал рядом с утверждением читается:
// видно сразу, какой ответ разбирается и что от него ждут. Часть из них к тому же
// стенд не отдаёт вовсе — например отбитый переход.

const (
	selfData = `{"contract_version":"2026-09-24","environment":"sandbox","network_id":"net_1",
		"scopes":["cpa:self:read","cpa:partners:write"],"agent_base":"margin",
		"agent_hold":{"mode":"own_days","days":14},"min_payout_rub":1000,
		"rate_limit_per_min":3000,"webhook_configured":false}`

	webhookData = `{"url":"https://hook.example/cpa",
		"events":["cpa.conversion.approved","cpa.payout.paid"],"secret":"whsec_shown_once"}`

	partnerData = `{"external_id":"blogger-42","tenant_recca_id":"usr_op4q","role":"webmaster",
		"partner_state":"active","status":"active","recruiter_external_id":null,
		"created_at":"2026-09-21T00:00:00.000Z"}`

	balanceData = `{"available_rub":1500,"on_hold_rub":700,"paid_total_rub":3200,
		"min_payout_rub":1000,"can_request":true}`

	offerPartnerData = `{"id":"off_1","name":"Подписка","advertiser_id":"adv_1",
		"payout_model":"cpa","payout_rate":{"kind":"fixed","value_rub":500,"value_percent":null},
		"goals":[{"code":"1","name":"Заказ","is_milestone":false,
			"payout_rate":{"kind":"fixed","value_rub":500,"value_percent":null},"hold_days":30}],
		"geo":{"mode":"allow","countries":["RU","BY"]},
		"allowed_traffic":["social","seo"],"disallowed_traffic":["brand"],
		"hold_days":30,"attribution_window_days":30}`

	offerAdvertiserData = `{"id":"off_1","name":"Подписка","advertiser_id":"adv_1",
		"payout_model":"cpa","revenue_rate":{"kind":"fixed","value_rub":800,"value_percent":null},
		"goals":[{"code":"1","name":"Заказ","is_milestone":false,
			"revenue_rate":{"kind":"fixed","value_rub":800,"value_percent":null},"hold_days":30}],
		"geo":{"mode":"allow","countries":["RU"]},"hold_days":30,"attribution_window_days":30}`

	creativeData = `{"id":"cr_1","kind":"landing","title":"Главная","description":null,
		"url":"https://adv.example/","text":null,"mime_type":null,"width":null,"height":null,
		"is_default":true,"created_at":"2026-09-21T00:00:00.000Z"}`

	linkData = `{"code":"lnk_1","target_url":"https://api.recca.ru/go/lnk_1",
		"partner_external_id":"blogger-42","offer_id":"off_1",
		"created_at":"2026-09-21T00:00:00.000Z"}`

	clickData = `{"click_id":"clk_1","status":"open","reason":null,
		"redirect_url":"https://adv.example/?c=clk_1","expires_at":"2026-10-21T00:00:00.000Z",
		"created_at":"2026-09-21T00:00:00.000Z","offer_id":"off_1",
		"partner_external_id":"blogger-42"}`

	conversionRowData = `{"external_id":"order-1","goal":"1","status":"hold",
		"partner_external_id":"blogger-42","offer_id":"off_1","advertiser_id":"adv_1",
		"payout_rub":500,"sum_rub":12.5,"sub_id":"tg",
		"created_at":"2026-09-21T00:00:00.000Z","occurred_at":null,
		"hold_until":"2026-09-28T00:00:00.000Z"}`

	payoutData = `{"id":"po_1","partner_external_id":"blogger-42","amount_rub":1500,
		"status":"requested","period_from":"2026-09-01","period_to":"2026-09-30",
		"created_at":"2026-09-21T00:00:00.000Z","paid_at":null}`

	statisticsData = `{"key":"off_1","clicks":120,"conversions":8,"approved":6,
		"payout_rub":4000,"revenue_rub":6400}`
)

// conversionDetailData — карточка с тройкой ставок. Отдельно от строки списка:
// списку маржа и выручка не достаются по построению.
const conversionDetailData = `{"external_id":"order-1","goal":"1","status":"hold",
	"partner_external_id":"blogger-42","offer_id":"off_1","advertiser_id":"adv_1",
	"payout_rub":500,"revenue_rub":800,"margin_rub":300,"sum_rub":12.5,"sub_id":"tg",
	"created_at":"2026-09-21T00:00:00.000Z","occurred_at":null,
	"hold_until":"2026-09-28T00:00:00.000Z","agent_leg":null,"fraud_flags":[]}`

// ── таблица ──────────────────────────────────────────────────────────────────

type methodCase struct {
	// covers — «Сервис.Метод»; по нему сверяется знаменатель.
	covers string
	// data — что дверь отдаёт в data.
	data string
	// invoke зовёт метод.
	invoke func(context.Context, *cpa.Client) error

	wantMethod string
	wantPath   string
	// wantQuery — пары, которые ОБЯЗАНЫ быть в строке запроса.
	wantQuery []string
	// wantBody — тело дословно; пустая строка означает «тела быть не должно».
	wantBody string
	// idempotent — объявлена ли операция повторяемой.
	idempotent bool
}

func mustPayout(t *testing.T) cpa.PayoutParams {
	t.Helper()
	p, err := cpa.NewPayoutParams("blogger-42", 1500,
		cpa.NewDate(2026, time.September, 1), cpa.NewDate(2026, time.September, 30))
	if err != nil {
		t.Fatalf("NewPayoutParams: %v", err)
	}
	return p
}

func methodCases(t *testing.T) []methodCase {
	t.Helper()
	payout := mustPayout(t)
	from := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)

	return []methodCase{
		{
			covers: "Self.Get", data: selfData,
			invoke:     func(ctx context.Context, c *cpa.Client) error { _, _, err := c.Self.Get(ctx); return err },
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/self",
			idempotent: true,
		},
		{
			covers: "Self.SetWebhook", data: webhookData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Self.SetWebhook(ctx, cpa.SetWebhookParams{URL: "https://hook.example/cpa"})
				return err
			},
			wantMethod: http.MethodPut, wantPath: "/federation/v1/cpa/self/webhook",
			wantBody: `{"url":"https://hook.example/cpa"}`,
			// ⚠⚠ НЕ повторяется: в ответе единственный показ секрета.
			idempotent: false,
		},
		{
			covers: "Partners.Upsert", data: partnerData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
					ExternalID: "blogger-42", Role: cpa.PartnerWebmaster,
				})
				return err
			},
			wantMethod: http.MethodPost, wantPath: "/federation/v1/cpa/partners",
			wantBody:   `{"external_id":"blogger-42","role":"webmaster"}`,
			idempotent: true,
		},
		{
			covers: "Partners.List", data: "[" + partnerData + "]",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Partners.List(ctx, cpa.ListPartnersParams{
					Role: cpa.PartnerAgent, RecruiterExternalID: "agent-1",
					PageParams: cpa.PageParams{Limit: 50, Offset: 100},
				})
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/partners",
			wantQuery:  []string{"role=agent", "recruiter_external_id=agent-1", "limit=50", "offset=100"},
			idempotent: true,
		},
		{
			covers: "Partners.Get", data: partnerData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Partners.Get(ctx, "blogger-42")
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/partners/blogger-42",
			idempotent: true,
		},
		{
			covers: "Partners.Balance", data: balanceData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Partners.Balance(ctx, "blogger-42")
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/partners/blogger-42/balance",
			idempotent: true,
		},
		{
			covers: "Offers.ListForPartner", data: "[" + offerPartnerData + "]",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Offers.ListForPartner(ctx, cpa.ListOffersParams{})
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/offers",
			wantQuery:  []string{"view=partner"},
			idempotent: true,
		},
		{
			covers: "Offers.ListForAdvertiser", data: "[" + offerAdvertiserData + "]",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Offers.ListForAdvertiser(ctx, "adv_1", cpa.ListOffersParams{})
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/offers",
			wantQuery:  []string{"view=advertiser", "advertiser_id=adv_1"},
			idempotent: true,
		},
		{
			covers: "Offers.GetForPartner", data: offerPartnerData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Offers.GetForPartner(ctx, "off_1")
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/offers/off_1",
			wantQuery:  []string{"view=partner"},
			idempotent: true,
		},
		{
			covers: "Offers.GetForAdvertiser", data: offerAdvertiserData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Offers.GetForAdvertiser(ctx, "off_1")
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/offers/off_1",
			wantQuery:  []string{"view=advertiser"},
			idempotent: true,
		},
		{
			covers: "Offers.Creatives", data: "[" + creativeData + "]",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Offers.Creatives(ctx, "off_1")
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/offers/off_1/creatives",
			idempotent: true,
		},
		{
			covers: "Links.Create", data: linkData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Links.Create(ctx, cpa.CreateLinkParams{
					PartnerExternalID: "blogger-42", OfferID: "off_1",
				})
				return err
			},
			wantMethod: http.MethodPost, wantPath: "/federation/v1/cpa/links",
			wantBody:   `{"partner_external_id":"blogger-42","offer_id":"off_1"}`,
			idempotent: true,
		},
		{
			covers: "Clicks.Create", data: clickData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Clicks.Create(ctx, cpa.CreateClickParams{
					PartnerExternalID: "blogger-42", OfferID: "off_1",
					ClientClickID: "cc-1", SubID: "tg", Country: "RU",
				})
				return err
			},
			wantMethod: http.MethodPost, wantPath: "/federation/v1/cpa/clicks",
			wantBody: `{"partner_external_id":"blogger-42","offer_id":"off_1",` +
				`"client_click_id":"cc-1","sub_id":"tg","country":"RU"}`,
			// ⚠⚠ НЕ повторяется: повтор есть второй переход.
			idempotent: false,
		},
		{
			covers: "Conversions.Create", data: conversionDetailData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
					ClickID: "clk_1", ExternalID: "order-1", Status: "approved",
				})
				return err
			},
			wantMethod: http.MethodPost, wantPath: "/federation/v1/cpa/conversions",
			wantBody:   `{"click_id":"clk_1","external_id":"order-1","status":"approved"}`,
			idempotent: true,
		},
		{
			covers: "Conversions.List", data: "[" + conversionRowData + "]",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Conversions.List(ctx, cpa.ListConversionsParams{
					PartnerExternalID: "blogger-42", Status: cpa.ConversionHold, From: &from,
				})
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/conversions",
			wantQuery: []string{
				"partner_external_id=blogger-42", "status=hold",
				"from=2026-09-01T10%3A00%3A00Z",
			},
			idempotent: true,
		},
		{
			covers: "Conversions.Get", data: conversionDetailData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Conversions.Get(ctx, cpa.GetConversionParams{
					ExternalID: "order-1", Goal: cpa.Ptr("1"),
				})
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/conversions/order-1",
			wantQuery:  []string{"goal=1"},
			idempotent: true,
		},
		{
			covers: "Conversions.Cancel", data: conversionDetailData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Conversions.Cancel(ctx, cpa.CancelConversionParams{
					ExternalID: "order-1", Reason: "возврат",
				})
				return err
			},
			wantMethod: http.MethodPost, wantPath: "/federation/v1/cpa/conversions/order-1/cancel",
			wantBody:   `{"reason":"возврат"}`,
			idempotent: true,
		},
		{
			covers: "Payouts.Create", data: payoutData,
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Payouts.Create(ctx, payout)
				return err
			},
			wantMethod: http.MethodPost, wantPath: "/federation/v1/cpa/payouts",
			wantBody: `{"partner_external_id":"blogger-42","amount_rub":1500,` +
				`"period_from":"2026-09-01","period_to":"2026-09-30"}`,
			idempotent: true,
		},
		{
			covers: "Payouts.List", data: "[" + payoutData + "]",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Payouts.List(ctx, cpa.ListPayoutsParams{Status: cpa.PayoutPaid})
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/payouts",
			wantQuery:  []string{"status=paid"},
			idempotent: true,
		},
		{
			covers: "Statistics.Get", data: "[" + statisticsData + "]",
			invoke: func(ctx context.Context, c *cpa.Client) error {
				_, _, err := c.Statistics.Get(ctx, cpa.StatisticsParams{
					GroupBy: cpa.StatisticsByOffer,
					From:    cpa.Ptr(cpa.NewDate(2026, time.September, 1)),
				})
				return err
			},
			wantMethod: http.MethodGet, wantPath: "/federation/v1/cpa/statistics",
			wantQuery:  []string{"group_by=offer", "from=2026-09-01"},
			idempotent: true,
		},
	}
}

// Каждый метод шлёт то, что обещает контракт.
func TestEveryMethodSendsContractRequest(t *testing.T) {
	for _, tc := range methodCases(t) {
		t.Run(tc.covers, func(t *testing.T) {
			stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf(tc.data)}}}
			c := stand.client(t)

			if err := tc.invoke(context.Background(), c); err != nil {
				t.Fatalf("вызов вернул ошибку: %v", err)
			}
			if got := stand.attempts.Load(); got != 1 {
				t.Fatalf("попыток %d, ждали одну", got)
			}
			if stand.methods[0] != tc.wantMethod {
				t.Errorf("метод %s, ждали %s", stand.methods[0], tc.wantMethod)
			}
			if stand.paths[0] != tc.wantPath {
				t.Errorf("путь %s, ждали %s", stand.paths[0], tc.wantPath)
			}
			for _, want := range tc.wantQuery {
				if !strings.Contains(stand.queries[0], want) {
					t.Errorf("в строке запроса нет %q: %q", want, stand.queries[0])
				}
			}
			body := strings.TrimSpace(stand.bodies[0])
			switch {
			case tc.wantBody == "" && body != "":
				t.Errorf("тело не ожидалось, пришло %s", body)
			case tc.wantBody != "" && body != tc.wantBody:
				t.Errorf("тело:\n  ушло:  %s\n  ждали: %s", body, tc.wantBody)
			}
		})
	}
}

// ⚠⚠ Политика повтора — по ОПЕРАЦИИ. Чтения повторяются всегда; записи — только
// объявившие себя идемпотентными. Не повторяются две: клик (повтор есть второй
// переход) и установка вебхука (в ответе единственный показ секрета).
func TestEveryMethodFollowsItsRetryPolicy(t *testing.T) {
	for _, tc := range methodCases(t) {
		t.Run(tc.covers, func(t *testing.T) {
			stand := &apiStand{steps: []apiStep{
				{status: http.StatusServiceUnavailable, body: failEnvelope},
				{status: http.StatusOK, body: okEnvelopeOf(tc.data)},
			}}
			c := stand.client(t)

			err := tc.invoke(context.Background(), c)

			want := int32(1)
			if tc.idempotent {
				want = 2
			}
			if got := stand.attempts.Load(); got != want {
				t.Errorf("попыток %d, ждали %d (idempotent=%v)", got, want, tc.idempotent)
			}
			if tc.idempotent && err != nil {
				t.Errorf("повторяемая операция не пережила сбой: %v", err)
			}
			if !tc.idempotent && err == nil {
				t.Error("неповторяемая операция вернула успех после 503 — значит её повторили")
			}
		})
	}
}

// ⚠⚠ Знаменатель таблицы. Без него «все методы покрыты» означало бы «покрыто
// столько, сколько вписали», и новый метод следующей волны молча остался бы без
// случая. Перечень собирается РЕФЛЕКСИЕЙ по полям клиента, поэтому новый сервис
// попадает под проверку сам.
func TestRequestTableCoversEveryMethod(t *testing.T) {
	t.Parallel()

	covered := map[string]bool{}
	for _, tc := range methodCases(t) {
		if covered[tc.covers] {
			t.Errorf("случай %s объявлен дважды", tc.covers)
		}
		covered[tc.covers] = true
	}

	var missing, services []string
	clientType := reflect.TypeOf(cpa.Client{})
	for i := range clientType.NumField() {
		field := clientType.Field(i)
		if !field.IsExported() || field.Type.Kind() != reflect.Pointer {
			continue
		}
		serviceType := field.Type.Elem()
		if serviceType.Kind() != reflect.Struct || !strings.HasSuffix(serviceType.Name(), "Service") {
			continue
		}
		services = append(services, field.Name)

		for m := range field.Type.NumMethod() {
			name := field.Name + "." + field.Type.Method(m).Name
			if !covered[name] {
				missing = append(missing, name)
			}
			delete(covered, name)
		}
	}

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("методы без случая в таблице: %s", strings.Join(missing, ", "))
	}
	for name := range covered {
		t.Errorf("случай %s описывает метод, которого у клиента нет", name)
	}
	// Проверка, которой нечего проверять, зелена по пустоте.
	if len(services) == 0 {
		t.Fatal("у клиента не найдено ни одного сервиса — обход полей сломан")
	}
	if len(services) != 8 {
		t.Errorf("сервисов %d (%s), ждали 8: перечень эпика называет восемь", len(services), strings.Join(services, ", "))
	}
}

// ⚠⚠ external_id конверсии — строка до 200 знаков БЕЗ ограничения алфавита, в
// отличие от идентификатора партнёра. Слеш и вопрос в ней законны, и подставленные
// в путь как есть они превратили бы адрес в другой путь с параметрами: дверь
// ответила бы про другую конверсию либо 404, и ни одна проверка формы этого не
// заметила бы.
func TestConversionExternalIDIsEscapedInPath(t *testing.T) {
	stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf(conversionDetailData)}}}
	c := stand.client(t)

	_, _, err := c.Conversions.Get(context.Background(), cpa.GetConversionParams{
		ExternalID: "shop/17?x=1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	const want = "/federation/v1/cpa/conversions/shop%2F17%3Fx=1"
	if stand.paths[0] != want {
		t.Errorf("путь %q, ждали %q", stand.paths[0], want)
	}
	if stand.queries[0] != "" {
		t.Errorf("часть идентификатора уехала в строку запроса: %q", stand.queries[0])
	}
}

// Короткие имена обязаны покрывать типы контракта: иначе квикстарту понадобился бы
// второй импорт, а обещание «одного импорта достаточно» тихо перестало бы
// выполняться.
func TestShortNamesCoverContractTypes(t *testing.T) {
	t.Parallel()

	// Берём по одному значению каждого типа, названного в types.go, и проверяем,
	// что оно собирается ПОД КОРОТКИМ ИМЕНЕМ. Компиляция этого теста и есть
	// проверка; пустое тело было бы зелено по пустоте, поэтому значения ещё и
	// сверяются.
	var (
		_ cpa.SelfInfo
		_ cpa.WebhookConfig
		_ cpa.Partner
		_ cpa.PartnerBalance
		_ cpa.OfferPartnerView
		_ cpa.OfferAdvertiserView
		_ cpa.Creative
		_ cpa.Link
		_ cpa.Click
		_ cpa.Conversion
		_ cpa.ConversionDetail
		_ cpa.Payout
		_ cpa.StatisticsRow
	)
	if cpa.PartnerWebmaster != "webmaster" || cpa.ClickTrafficback != "trafficback" {
		t.Error("словарь под коротким именем разошёлся со значением контракта")
	}
	if got := cpa.NewDate(2026, time.September, 1).String(); got != "2026-09-01" {
		t.Errorf("Date.String = %q, ждали 2026-09-01", got)
	}
}

// Разбор ответа: страница и карточка доезжают до вызывающего целиком.
func TestResponsesAreParsed(t *testing.T) {
	t.Run("баланс партнёра", func(t *testing.T) {
		stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf(balanceData)}}}
		balance, resp, err := stand.client(t).Partners.Balance(context.Background(), "blogger-42")
		if err != nil {
			t.Fatalf("Balance: %v", err)
		}
		if balance.AvailableRUB != 1500 || balance.OnHoldRUB != 700 || balance.MinPayoutRUB != 1000 {
			t.Errorf("разобрано %+v", balance)
		}
		if !balance.CanRequest {
			t.Error("can_request потерян — а это ответ двери на вопрос «хватит ли на заявку»")
		}
		if resp.RequestID != "req_1" {
			t.Errorf("request_id = %q", resp.RequestID)
		}
	})

	t.Run("оффер в партнёрской проекции", func(t *testing.T) {
		stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf(offerPartnerData)}}}
		offer, _, err := stand.client(t).Offers.GetForPartner(context.Background(), "off_1")
		if err != nil {
			t.Fatalf("GetForPartner: %v", err)
		}
		if offer.PayoutRate.Kind != cpa.RateFixed || offer.PayoutRate.ValueRUB == nil || *offer.PayoutRate.ValueRUB != 500 {
			t.Errorf("ставка разобрана неверно: %+v", offer.PayoutRate)
		}
		// ⚠⚠ Режим гео, а не просто список: прочитав deny как allow, интегратор
		// полил бы трафик туда, откуда его отбивают.
		if offer.Geo.Mode != cpa.GeoAllow || len(offer.Geo.Countries) != 2 {
			t.Errorf("гео разобрано неверно: %+v", offer.Geo)
		}
		if len(offer.Goals) != 1 || offer.Goals[0].Code != "1" {
			t.Errorf("цели разобраны неверно: %+v", offer.Goals)
		}
	})

	t.Run("строка отчёта", func(t *testing.T) {
		stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf("[" + statisticsData + "]")}}}
		rows, _, err := stand.client(t).Statistics.Get(context.Background(), cpa.StatisticsParams{
			GroupBy: cpa.StatisticsByOffer,
		})
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(rows) != 1 || rows[0].Clicks != 120 || rows[0].PayoutRUB != 4000 {
			t.Fatalf("разобрано %+v", rows)
		}
		// Ключ этой двери принадлежит оператору, и выручка приходит в каждой
		// строке — она обязана доехать до вызывающего числом.
		if rows[0].RevenueRUB != 6400 {
			t.Errorf("revenue_rub потерян: %v", rows[0].RevenueRUB)
		}
		if rows[0].Approved != 6 {
			t.Errorf("approved потерян: %v", rows[0].Approved)
		}
	})

	// Контракт 2026-09-24: approved и revenue_rub обязательны. Строка без них —
	// ErrIncompleteData, а не нули: «выручка 0 ₽» вместо отказа была бы
	// выдуманным числом в отчёте оператора.
	for _, field := range []string{"approved", "revenue_rub"} {
		t.Run("строка отчёта без "+field, func(t *testing.T) {
			var row map[string]any
			if err := json.Unmarshal([]byte(statisticsData), &row); err != nil {
				t.Fatal(err)
			}
			delete(row, field)
			body, _ := json.Marshal(row)
			stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf("[" + string(body) + "]")}}}
			_, _, err := stand.client(t).Statistics.Get(context.Background(), cpa.StatisticsParams{
				GroupBy: cpa.StatisticsByOffer,
			})
			if !errors.Is(err, cpa.ErrIncompleteData) {
				t.Errorf("ждали ErrIncompleteData, получили %v", err)
			}
		})
	}

	t.Run("заявка на выплату с датами без времени", func(t *testing.T) {
		stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf("[" + payoutData + "]")}}}
		payouts, _, err := stand.client(t).Payouts.List(context.Background(), cpa.ListPayoutsParams{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(payouts) != 1 {
			t.Fatalf("строк %d", len(payouts))
		}
		// ⚠ time.Time здесь упал бы: дверь присылает «2026-09-01», а encoding/json
		// разбирает time.Time только как RFC 3339.
		if got := payouts[0].PeriodFrom.String(); got != "2026-09-01" {
			t.Errorf("period_from = %q", got)
		}
		if payouts[0].PaidAt != nil {
			t.Error("paid_at = null обязан остаться nil: «ещё не выплачено» и «выплачено в нулевое время» — разные факты")
		}
	})
}

// Тело запроса не несёт ключей, которых не задавали: дверь объявлена strict, и
// лишнее поле она отбивает 400 INVALID_INPUT.
func TestRequestBodiesCarryOnlyWhatWasSet(t *testing.T) {
	stand := &apiStand{steps: []apiStep{{status: http.StatusOK, body: okEnvelopeOf(partnerData)}}}
	c := stand.client(t)

	_, _, err := c.Partners.Upsert(context.Background(), cpa.UpsertPartnerParams{
		ExternalID: "blogger-42", Role: cpa.PartnerAgent,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stand.bodies[0]), &body); err != nil {
		t.Fatalf("тело не разобрано: %v", err)
	}
	for _, unexpected := range []string{"partner_state", "recruiter_external_id", "agent_percent"} {
		if _, present := body[unexpected]; present {
			t.Errorf("в теле есть %s, которого не задавали — дверь strict и отбьёт такое тело", unexpected)
		}
	}
	if len(body) != 2 {
		t.Errorf("ключей в теле %d, ждали два: %s", len(body), stand.bodies[0])
	}
}
