// Контракт первой вертикали: POST /cpa/conversions — приём результата.
//
// Источник — docs/federation/cpa-api.md (разделы 6–9) и openapi-cpa.yaml
// (`createCpaConversion`).
package cpa_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.recca.ru/cpa"
)

// conversionData — карточка конверсии в ответе двери: тройка ставок сходится,
// нога рекрутёра есть.
const conversionData = `{
	"external_id":"order-0751","goal":"1","status":"hold",
	"partner_external_id":"blogger-42","offer_id":"off_1","advertiser_id":"adv_1",
	"payout_rub":500,"revenue_rub":800,"margin_rub":300,
	"sum_rub":12.5,"sub_id":"tg",
	"created_at":"2026-09-21T00:00:00.000Z","occurred_at":null,
	"hold_until":"2026-09-28T00:00:00.000Z",
	"agent_leg":{"external_id":"agent-1","amount_rub":60,"base":"margin","percent":20,"hold_until":null},
	"fraud_flags":[]
}`

func conversionEnvelope(requestID string) string {
	return `{"success":true,"data":` + conversionData + `,"meta":{"request_id":"` + requestID + `"}}`
}

// conversionStand — стенд, записывающий запросы и отвечающий по сценарию.
type conversionStand struct {
	attempts atomic.Int32
	bodies   []string
	paths    []string
	methods  []string
	steps    []conversionStep
}

type conversionStep struct {
	status int
	body   string
}

func (s *conversionStand) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.attempts.Add(1)) - 1
		raw, _ := io.ReadAll(r.Body)
		s.bodies = append(s.bodies, string(raw))
		s.paths = append(s.paths, r.URL.Path)
		s.methods = append(s.methods, r.Method)

		step := s.steps[len(s.steps)-1]
		if n < len(s.steps) {
			step = s.steps[n]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(step.status)
		_, _ = io.WriteString(w, step.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newClientFor(t *testing.T, srv *httptest.Server) *cpa.Client {
	t.Helper()
	c, err := cpa.New("tk_test_key", &cpa.ClientOptions{BaseURL: srv.URL, MaxRetries: cpa.Ptr(1)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func validParams() cpa.CreateConversionParams {
	return cpa.CreateConversionParams{
		ClickID:    "clk_01K5",
		ExternalID: "order-0751",
		Status:     "approved",
	}
}

// Метод шлёт то, что обещает контракт: POST по версионированному пути и тело, в
// котором присутствие ключа значимо.
func TestConversionsCreateSendsContractBody(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*cpa.CreateConversionParams)
		want   string
	}{
		{
			name:   "цель не передана — ключа в теле нет",
			mutate: func(p *cpa.CreateConversionParams) {},
			want:   `{"click_id":"clk_01K5","external_id":"order-0751","status":"approved"}`,
		},
		{
			name:   "цель передана пустой — ключ есть",
			mutate: func(p *cpa.CreateConversionParams) { p.Goal = cpa.Ptr("") },
			want:   `{"click_id":"clk_01K5","external_id":"order-0751","status":"approved","goal":""}`,
		},
		{
			// ⚠ Сумма уходит ЧИСЛОМ, а не строкой: строка сломала бы строгий
			// разбор тела на двери (неизвестный тип — 400 INVALID_INPUT).
			name:   "сумма целыми рублями",
			mutate: func(p *cpa.CreateConversionParams) { p.SumRUB = cpa.RUBPtr(10000) },
			want:   `{"click_id":"clk_01K5","external_id":"order-0751","status":"approved","sum_rub":10000}`,
		},
		{
			name: "полный набор",
			mutate: func(p *cpa.CreateConversionParams) {
				p.Goal = cpa.Ptr("2")
				p.SumRUB = cpa.RUBPtr(10000)
				p.SubID = "tg"
				p.OccurredAt = cpa.Ptr(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC))
			},
			want: `{"click_id":"clk_01K5","external_id":"order-0751","status":"approved","goal":"2","sum_rub":10000,"sub_id":"tg","occurred_at":"2026-09-21T00:00:00Z"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stand := &conversionStand{steps: []conversionStep{{status: 201, body: conversionEnvelope("req_1")}}}
			srv := stand.start(t)
			c := newClientFor(t, srv)

			params := validParams()
			tc.mutate(&params)

			if _, _, err := c.Conversions.Create(context.Background(), params); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if got, want := stand.methods[0], http.MethodPost; got != want {
				t.Errorf("метод %s, ждали %s", got, want)
			}
			if got, want := stand.paths[0], "/federation/v1/cpa/conversions"; got != want {
				t.Errorf("путь %q, ждали %q", got, want)
			}
			if got := stand.bodies[0]; got != tc.want {
				t.Errorf("тело:\n  дали  %s\n  ждали %s", got, tc.want)
			}
		})
	}
}

// Ответ разбирается целиком: тройка ставок, нога рекрутёра, статус и то, что
// пришло null, — nullʼом и остаётся.
func TestConversionsCreateParsesDetail(t *testing.T) {
	stand := &conversionStand{steps: []conversionStep{{status: 201, body: conversionEnvelope("req_created")}}}
	srv := stand.start(t)
	c := newClientFor(t, srv)

	conv, resp, err := c.Conversions.Create(context.Background(), validParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if conv.Status != cpa.ConversionHold {
		t.Errorf("Status = %q, ждали %q", conv.Status, cpa.ConversionHold)
	}
	if conv.PayoutRUB != 500 || conv.RevenueRUB != 800 || conv.MarginRUB != 300 {
		t.Errorf("ставки: payout %d, revenue %d, margin %d", conv.PayoutRUB, conv.RevenueRUB, conv.MarginRUB)
	}
	// ⚠ Сходимость тройки — доказательство приёмки по п. 3.4 договора, и тип
	// обязан донести все три числа, а не только выплату.
	if conv.PayoutRUB+conv.MarginRUB != conv.RevenueRUB {
		t.Error("тройка ставок не сходится — потребитель не сможет сверить расчёт")
	}
	if conv.SumRUB == nil || conv.SumRUB.String() != "12.5" {
		t.Errorf("SumRUB = %v, ждали лексическую форму 12.5", conv.SumRUB)
	}
	if conv.OccurredAt != nil {
		t.Error("OccurredAt не nil, а дверь прислала null — «не сказали, когда»")
	}
	if conv.AgentLeg == nil || conv.AgentLeg.AmountRUB != 60 {
		t.Errorf("AgentLeg = %+v", conv.AgentLeg)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, ждали 201", resp.StatusCode)
	}
	if resp.RequestID != "req_created" {
		t.Errorf("RequestID = %q", resp.RequestID)
	}
}

// ⚠ Идемпотентный повтор приходит с 200, а не 201, и это УСПЕХ: дверь вернула
// ту же конверсию. Клиент, считающий успехом только 201, сломался бы на первом
// же повторе после сетевого сбоя — то есть ровно тогда, когда идемпотентность и
// нужна.
func TestConversionsCreateAcceptsIdempotentRepeat(t *testing.T) {
	stand := &conversionStand{steps: []conversionStep{{status: 200, body: conversionEnvelope("req_repeat")}}}
	srv := stand.start(t)
	c := newClientFor(t, srv)

	conv, resp, err := c.Conversions.Create(context.Background(), validParams())
	if err != nil {
		t.Fatalf("идемпотентный повтор принят за отказ: %v", err)
	}
	if conv.ExternalID != "order-0751" {
		t.Errorf("ExternalID = %q", conv.ExternalID)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, ждали 200", resp.StatusCode)
	}
}

// ⚠⚠ Повтор (тот же статус) с другой суммой — отказ, а не успех: примитив, молча
// возвращающий существующую операцию под другую сумму, отчитался бы о деньгах,
// которых в кошельке нет. Смена статуса — переход, а не повтор, и сумму менять
// вправе; это решает дверь, клиент лишь доносит её отказ.
func TestConversionsCreateSurfacesIdempotentMismatch(t *testing.T) {
	const body = `{"success":false,"error":{"code":"IDEMPOTENT_MISMATCH","message":"amount differs","details":{"field":"sum_rub"}},"meta":{"request_id":"req_conflict"}}`
	stand := &conversionStand{steps: []conversionStep{{status: 409, body: body}}}
	srv := stand.start(t)
	c := newClientFor(t, srv)

	conv, resp, err := c.Conversions.Create(context.Background(), validParams())
	if err == nil {
		t.Fatal("409 пришёл без ошибки")
	}
	if conv != nil {
		t.Errorf("вместе с ошибкой вернулась конверсия: %+v", conv)
	}
	if !errors.Is(err, cpa.ErrIdempotentMismatch) {
		t.Errorf("errors.Is не нашёл сентинел: %v", err)
	}

	var apiErr *cpa.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ошибка не *cpa.APIError: %T", err)
	}
	if apiErr.RequestID != "req_conflict" {
		t.Errorf("RequestID = %q", apiErr.RequestID)
	}
	if len(apiErr.Details) == 0 {
		t.Error("Details пусты, а дверь назвала поле — интегратору нужно знать, что именно разошлось")
	}
	// ⚠ Отказ по занятому ключу повтор не переживает: сколько ни повторяй,
	// параметры останутся другими.
	if apiErr.Retryable {
		t.Error("IDEMPOTENT_MISMATCH помечен повторяемым")
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Errorf("ответ = %+v, ждали статус 409 — он нужен для диагностики", resp)
	}
}

// Отказ, который клиент может дать сам, не стоит ни запроса, ни чужого кода
// ошибки: в сеть не уходит НИЧЕГО.
func TestConversionsCreateValidatesBeforeNetwork(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*cpa.CreateConversionParams)
		wantField string
	}{
		{"нет click_id", func(p *cpa.CreateConversionParams) { p.ClickID = "" }, "click_id"},
		{"нет status", func(p *cpa.CreateConversionParams) { p.Status = "" }, "status"},
		{
			"external_id из зарезервированного пространства",
			func(p *cpa.CreateConversionParams) { p.ExternalID = cpa.ReservedExternalIDPrefix + "order-1" },
			"external_id",
		},
		{
			"сумма выше потолка контракта",
			func(p *cpa.CreateConversionParams) { p.SumRUB = cpa.RUBPtr(cpa.MaxSumRUB + 1) },
			"sum_rub",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stand := &conversionStand{steps: []conversionStep{{status: 201, body: conversionEnvelope("req_1")}}}
			srv := stand.start(t)
			c := newClientFor(t, srv)

			params := validParams()
			tc.mutate(&params)

			_, _, err := c.Conversions.Create(context.Background(), params)
			if err == nil {
				t.Fatal("негодный запрос ушёл в сеть и вернулся успехом")
			}
			if !errors.Is(err, cpa.ErrValidation) {
				t.Errorf("ошибка не помечена как проверка до сети: %v", err)
			}
			var v *cpa.ValidationError
			if errors.As(err, &v) && v.Field != tc.wantField {
				t.Errorf("поле %q, ждали %q", v.Field, tc.wantField)
			}
			if got := stand.attempts.Load(); got != 0 {
				t.Errorf("запросов %d, ждали 0 — проверка обязана быть ДО сети", got)
			}
		})
	}
}

// ⚠⚠ Приём конверсии идемпотентен по тройке «сеть + external_id + goal», и
// ПОЭТОМУ его POST повторяется на 5xx: повтор вернёт ту же конверсию, а не
// заплатит второй раз. Это единственная причина, по которой здесь можно то, что
// запрещено обычному Do.
func TestConversionsCreateRetriesIdempotentOperation(t *testing.T) {
	stand := &conversionStand{steps: []conversionStep{
		{status: 500, body: `{"success":false,"error":{"code":"INTERNAL_ERROR","message":"boom"}}`},
		{status: 201, body: conversionEnvelope("req_second_try")},
	}}
	srv := stand.start(t)
	c := newClientFor(t, srv)

	conv, resp, err := c.Conversions.Create(context.Background(), validParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := stand.attempts.Load(); got != 2 {
		t.Errorf("попыток %d, ждали 2 — идемпотентная операция обязана переживать сбой", got)
	}
	if conv.ExternalID != "order-0751" || resp.RequestID != "req_second_try" {
		t.Errorf("результат второй попытки потерян: %+v / %+v", conv, resp)
	}

	// ⚠ Тело второй попытки обязано быть тем же: пересобранное тело означало бы
	// другую операцию, а значит другой ключ идемпотентности.
	if len(stand.bodies) == 2 && stand.bodies[0] != stand.bodies[1] {
		t.Errorf("тела попыток разошлись:\n  1: %s\n  2: %s", stand.bodies[0], stand.bodies[1])
	}
}

// Успех без обещанных полей — нарушение контракта, а не «нулевая выплата».
// Разобрав такой ответ молча, клиент показал бы суммы, которых дверь не
// присылала.
func TestConversionsCreateRefusesIncompleteDetail(t *testing.T) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(conversionData), &fields); err != nil {
		t.Fatalf("фикстура не разобрана: %v", err)
	}
	delete(fields, "revenue_rub")
	trimmed, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	stand := &conversionStand{steps: []conversionStep{
		{status: 201, body: `{"success":true,"data":` + string(trimmed) + `,"meta":{"request_id":"req_partial"}}`},
	}}
	srv := stand.start(t)
	c := newClientFor(t, srv)

	conv, _, err := c.Conversions.Create(context.Background(), validParams())
	if err == nil {
		t.Fatalf("ответ без revenue_rub принят, конверсия = %+v", conv)
	}
	if !errors.Is(err, cpa.ErrIncompleteData) {
		t.Errorf("ошибка = %v, ждали ErrIncompleteData", err)
	}
	if !strings.Contains(err.Error(), "revenue_rub") {
		t.Errorf("ошибка не называет недостающее поле: %v", err)
	}
}
