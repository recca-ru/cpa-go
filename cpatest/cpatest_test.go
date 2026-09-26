// Контракт стенда и самого набора Conformance.
//
// ⚠⚠ Второй тест здесь важнее первого: он доказывает, что набор КРАСНЕЕТ. Набор,
// зелёный и на верном расчёте, и на сломанном, — утверждение о себе самом, и
// прогонять его незачем.
package cpatest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.recca.ru/cpa"
)

// recorder подменяет *testing.T: собирает то, что набор сообщил, вместо того
// чтобы ронять прогон.
type recorder struct {
	errors []string
	logs   []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

// Стенд отвечает конвертом контракта на КАЖДУЮ дверь, и спрашиваем мы его теми же
// методами SDK, которыми будет спрашивать интегратор: путь, собранный вручную,
// проверял бы стенд, а не связку.
func TestServerAnswersEveryDoor(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)
	ctx := context.Background()

	payout, err := cpa.NewPayoutParams("blogger-42", 1500,
		cpa.NewDate(2026, 9, 1), cpa.NewDate(2026, 9, 30))
	if err != nil {
		t.Fatalf("NewPayoutParams: %v", err)
	}

	doors := []struct {
		name   string
		path   string
		invoke func() error
	}{
		{"GET /cpa/self", "/cpa/self", func() error { _, _, err := c.Self.Get(ctx); return err }},
		{"PUT /cpa/self/webhook", "/cpa/self/webhook", func() error {
			_, _, err := c.Self.SetWebhook(ctx, cpa.SetWebhookParams{URL: "https://hook.example/cpa"})
			return err
		}},
		{"POST /cpa/partners", "/cpa/partners", func() error {
			_, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
				ExternalID: "blogger-42", Role: cpa.PartnerWebmaster,
			})
			return err
		}},
		{"GET /cpa/partners", "/cpa/partners", func() error {
			_, _, err := c.Partners.List(ctx, cpa.ListPartnersParams{})
			return err
		}},
		{"GET /cpa/partners/{id}", "/cpa/partners/blogger-42", func() error {
			_, _, err := c.Partners.Get(ctx, "blogger-42")
			return err
		}},
		{"GET /cpa/partners/{id}/balance", "/cpa/partners/blogger-42/balance", func() error {
			_, _, err := c.Partners.Balance(ctx, "blogger-42")
			return err
		}},
		{"GET /cpa/offers (partner)", "/cpa/offers", func() error {
			_, _, err := c.Offers.ListForPartner(ctx, cpa.ListOffersParams{})
			return err
		}},
		{"GET /cpa/offers (advertiser)", "/cpa/offers", func() error {
			_, _, err := c.Offers.ListForAdvertiser(ctx, "adv_1", cpa.ListOffersParams{})
			return err
		}},
		{"GET /cpa/offers/{id}", "/cpa/offers/off_1", func() error {
			_, _, err := c.Offers.GetForPartner(ctx, "off_1")
			return err
		}},
		{"GET /cpa/offers/{id}/creatives", "/cpa/offers/off_1/creatives", func() error {
			_, _, err := c.Offers.Creatives(ctx, "off_1")
			return err
		}},
		{"POST /cpa/links", "/cpa/links", func() error {
			_, _, err := c.Links.Create(ctx, cpa.CreateLinkParams{
				PartnerExternalID: "blogger-42", OfferID: "off_1",
			})
			return err
		}},
		{"POST /cpa/clicks", "/cpa/clicks", func() error {
			_, _, err := c.Clicks.Create(ctx, cpa.CreateClickParams{
				PartnerExternalID: "blogger-42", OfferID: "off_1",
			})
			return err
		}},
		{"POST /cpa/conversions", "/cpa/conversions", func() error {
			_, _, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
				ClickID: "clk_1", ExternalID: "door-order-1", Status: "approved",
			})
			return err
		}},
		{"GET /cpa/conversions", "/cpa/conversions", func() error {
			_, _, err := c.Conversions.List(ctx, cpa.ListConversionsParams{})
			return err
		}},
		{"GET /cpa/conversions/{id}", "/cpa/conversions/door-order-1", func() error {
			_, _, err := c.Conversions.Get(ctx, cpa.GetConversionParams{ExternalID: "door-order-1"})
			return err
		}},
		{"POST /cpa/conversions/{id}/cancel", "/cpa/conversions/door-order-1/cancel", func() error {
			_, _, err := c.Conversions.Cancel(ctx, cpa.CancelConversionParams{ExternalID: "door-order-1"})
			return err
		}},
		{"POST /cpa/payouts", "/cpa/payouts", func() error {
			_, _, err := c.Payouts.Create(ctx, payout)
			return err
		}},
		{"GET /cpa/payouts", "/cpa/payouts", func() error {
			_, _, err := c.Payouts.List(ctx, cpa.ListPayoutsParams{})
			return err
		}},
		{"GET /cpa/statistics", "/cpa/statistics", func() error {
			_, _, err := c.Statistics.Get(ctx, cpa.StatisticsParams{GroupBy: cpa.StatisticsByOffer})
			return err
		}},
	}

	for _, door := range doors {
		t.Run(door.name, func(t *testing.T) {
			if err := door.invoke(); err != nil {
				t.Errorf("стенд не ответил: %v", err)
			}
		})
	}

	// Знаменатель: восемнадцать дверей контракта.
	//
	// ⚠ Дверь — это ПАРА «метод + путь», а не путь: /cpa/partners,
	// /cpa/conversions и /cpa/payouts держат по две двери на одном пути. Считать
	// пути значило бы получить 15 и объявить это полнотой.
	doorsSeen := map[string]bool{}
	for _, r := range server.Requests() {
		doorsSeen[r.Method+" "+r.Path] = true
	}
	if len(doorsSeen) != 18 {
		var seen []string
		for d := range doorsSeen {
			seen = append(seen, d)
		}
		sort.Strings(seen)
		t.Errorf("различных дверей %d, ждали 18:\n  %s", len(doorsSeen), strings.Join(seen, "\n  "))
	}
	if len(server.Requests()) != len(doors) {
		t.Errorf("записано обращений %d, вызовов было %d", len(server.Requests()), len(doors))
	}
}

// ⚠ Заготовленный отказ срабатывает РОВНО ОДИН раз. Залипни он — и все следующие
// шаги цепочки покраснели бы по причине, которой в коде нет.
func TestPrepareErrorFiresExactlyOnce(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)
	ctx := context.Background()

	server.PrepareError(cpa.CodeProductPaused)

	_, _, err := c.Self.Get(ctx)
	if err == nil {
		t.Fatal("заготовленный отказ не сработал")
	}
	if !errorsIs(err, cpa.ErrProductPaused) {
		t.Errorf("отказ = %v, ждали ErrProductPaused", err)
	}

	if _, _, err := c.Self.Get(ctx); err != nil {
		t.Errorf("отказ сработал ВТОРОЙ раз: %v", err)
	}
}

// ⚠⚠ Удержанный отказ доходит до кода интегратора, который собрал СВОЕГО клиента с
// повторами, — ровно так, как это сделает facebase, направив cpa.New на
// server.URL(). Повторяемый код выбран намеренно: именно его одноразовая
// заготовка до вызывающего не доносит.
func TestHoldErrorReachesCallerThroughRetries(t *testing.T) {
	server := NewServer(t)
	c, err := cpa.New("tk_own", &cpa.ClientOptions{BaseURL: server.URL(), MaxRetries: cpa.Ptr(2)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	server.HoldError(cpa.CodeInternalError)

	_, _, err = c.Self.Get(ctx)
	if !errorsIs(err, cpa.ErrInternal) {
		t.Fatalf("удержанный отказ не дошёл до вызывающего: %v", err)
	}
	// Все три попытки (одна + два повтора) обязаны быть отбиты — иначе отказ
	// держался бы не на каждом запросе.
	if got := len(server.Requests()); got != 3 {
		t.Errorf("запросов %d, ждали 3: одна попытка и два повтора, все отбитые", got)
	}

	// Держится и на следующем вызове — до явного снятия.
	if _, _, err := c.Self.Get(ctx); err == nil {
		t.Error("отказ снялся сам — удержанный режим обязан держаться до ClearError")
	}

	server.ClearError()
	if _, _, err := c.Self.Get(ctx); err != nil {
		t.Errorf("после ClearError вызов по-прежнему отбит: %v", err)
	}
}

// Предел одноразовой заготовки, записанный в документации PrepareError, —
// закреплён тестом, а не только словами: у клиента с повторами отказ на
// повторяемом коде съедается, и вызов заканчивается успехом. Ради этого и заведён
// HoldError; поменяй поведение — и документация соврёт.
func TestPrepareErrorOnRetryableCodeIsEatenByOwnRetryingClient(t *testing.T) {
	server := NewServer(t)
	c, err := cpa.New("tk_own", &cpa.ClientOptions{BaseURL: server.URL(), MaxRetries: cpa.Ptr(2)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	server.PrepareError(cpa.CodeInternalError)
	if _, _, err := c.Self.Get(context.Background()); err != nil {
		t.Fatalf("поведение изменилось — перечитайте документацию PrepareError: %v", err)
	}
	if got := len(server.Requests()); got != 2 {
		t.Errorf("запросов %d, ждали 2: отбитая попытка и успешный повтор", got)
	}
}

// ClearError снимает и одноразовую заготовку, не дождавшись запроса.
func TestClearErrorCancelsOneShot(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)

	server.PrepareError(cpa.CodeProductPaused)
	server.ClearError()
	if _, _, err := c.Self.Get(context.Background()); err != nil {
		t.Errorf("снятая заготовка всё равно сработала: %v", err)
	}
}

// Код вне реестра контракта — ошибка вызова, и стенд обязан сказать об этом, а не
// придумать статус.
func TestPrepareErrorRefusesUnknownCode(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)

	server.PrepareError("НЕ_ИЗ_РЕЕСТРА")
	_, _, err := c.Self.Get(context.Background())
	if err == nil {
		t.Fatal("код вне реестра принят молча")
	}
	if !strings.Contains(err.Error(), "реестру") {
		t.Errorf("стенд не объяснил, что код не из реестра: %v", err)
	}
}

// Requests() записывает тело — на нём строятся утверждения потребителя.
func TestRequestsRecordBody(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)

	_, _, err := c.Partners.Upsert(context.Background(), cpa.UpsertPartnerParams{
		ExternalID: "blogger-42", Role: cpa.PartnerAgent,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("обращений %d", len(requests))
	}
	if got, want := requests[0].BodyString(), `{"external_id":"blogger-42","role":"agent"}`; got != want {
		t.Errorf("тело %s, ждали %s", got, want)
	}
	if requests[0].Method != http.MethodPost || requests[0].Path != "/cpa/partners" {
		t.Errorf("записано %s %s", requests[0].Method, requests[0].Path)
	}
}

// Стенд возвращает ТО, о чём его спросили: иначе утверждения потребителя были бы
// зелены на любом запросе.
func TestServerEchoesWhatWasAsked(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)

	conversion, resp, err := c.Conversions.Create(context.Background(), cpa.CreateConversionParams{
		ClickID: "clk_1", ExternalID: "echo-1", Status: "approved", Goal: cpa.Ptr("webinar"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if conversion.ExternalID != "echo-1" || conversion.Goal != "webinar" {
		t.Errorf("стенд вернул чужую конверсию: %s / %s", conversion.ExternalID, conversion.Goal)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("первый приём ответил %d, ждали 201", resp.StatusCode)
	}

	// ⚠⚠ Повтор — 200 и ТА ЖЕ строка: это единственная логика стенда кроме отдачи
	// фикстур, и без неё Conformance нельзя было бы прогнать против него.
	repeat, repeatResp, err := c.Conversions.Create(context.Background(), cpa.CreateConversionParams{
		ClickID: "clk_1", ExternalID: "echo-1", Status: "approved", Goal: cpa.Ptr("webinar"),
	})
	if err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if repeatResp.StatusCode != http.StatusOK {
		t.Errorf("повтор ответил %d, ждали 200", repeatResp.StatusCode)
	}
	if repeat.ExternalID != conversion.ExternalID {
		t.Errorf("повтор вернул другую конверсию: %s", repeat.ExternalID)
	}
}

// ⚠⚠ Стенд обязан отдавать в СПИСКЕ проекцию списка, а не карточку. Найдено
// мутацией: типизированный Conversion лишние поля просто игнорирует — это верное
// поведение клиента (новое поле в ответе совместимо), — и поэтому подмена
// проекции в стенде не роняла НИ ОДНОГО теста. Дыра была ровно там, где контракт
// скрывает маржу оператора: стенд отвечал бы не так, как живая дверь, и код,
// разбирающий список своими руками, увидел бы выручку, которой в бою нет.
func TestServerListDoesNotLeakTripleRates(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)

	var rows []map[string]json.RawMessage
	if _, err := c.Do(context.Background(), http.MethodGet, "/cpa/conversions", nil, &rows); err != nil {
		t.Fatalf("список конверсий: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("страница пуста — проверять нечего")
	}
	for _, forbidden := range []string{"revenue_rub", "margin_rub", "agent_leg"} {
		if _, present := rows[0][forbidden]; present {
			t.Errorf("в строке списка есть %s — живая дверь этого не отдаёт, проекции разные", forbidden)
		}
	}
	// Запрет доказывает разрешение: выплата в списке БЫТЬ обязана, иначе проверка
	// выше зелена на пустой строке.
	if _, present := rows[0]["payout_rub"]; !present {
		t.Error("в строке списка нет payout_rub — проверка выше зелена по пустоте")
	}
}

// Повтор заявки с ДРУГОЙ суммой — отказ, а не вторая заявка.
func TestServerRefusesPayoutOnMismatchedAmount(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)
	ctx := context.Background()

	from, to := cpa.NewDate(2026, 9, 1), cpa.NewDate(2026, 9, 30)
	first, err := cpa.NewPayoutParams("blogger-42", 1500, from, to)
	if err != nil {
		t.Fatalf("NewPayoutParams: %v", err)
	}
	if _, _, err := c.Payouts.Create(ctx, first); err != nil {
		t.Fatalf("первая заявка: %v", err)
	}

	other, err := cpa.NewPayoutParams("blogger-42", 2500, from, to)
	if err != nil {
		t.Fatalf("NewPayoutParams: %v", err)
	}
	_, _, err = c.Payouts.Create(ctx, other)
	if !errorsIs(err, cpa.ErrIdempotentMismatch) {
		t.Errorf("повтор с другой суммой дал %v, ждали IDEMPOTENT_MISMATCH", err)
	}
}

// ⚠⚠ ГЛАВНЫЙ тест пакета: набор зелен против стенда И краснеет, когда стенд
// отдаёт несходящуюся тройку ставок. Без второй половины первая доказывает не
// больше, чем пустой тест.
func TestConformanceIsGreenOnStandAndRedOnBrokenTriple(t *testing.T) {
	t.Run("зелёный против стенда", func(t *testing.T) {
		server := NewServer(t)
		rec := &recorder{}

		Conformance(rec, server.Client(t), ConformanceOptions{})

		if len(rec.errors) > 0 {
			t.Errorf("набор нашёл %d проблем на исправном стенде:\n  %s",
				len(rec.errors), strings.Join(rec.errors, "\n  "))
		}
		if len(rec.logs) == 0 {
			t.Error("набор не сообщил ни одного замера — он либо не шёл, либо молчит о том, что прошёл")
		}
	})

	t.Run("красный на несходящейся тройке", func(t *testing.T) {
		server := NewServer(t)
		// Маржа +1: выплата + маржа перестаёт равняться выручке. Ровно то
		// расхождение, ради которого п. 3.4 договора и написан.
		server.tamper = func(raw json.RawMessage) json.RawMessage {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatalf("карточка не разобрана: %v", err)
			}
			var margin int64
			if err := json.Unmarshal(object["margin_rub"], &margin); err != nil {
				t.Fatalf("маржа не разобрана: %v", err)
			}
			object["margin_rub"] = json.RawMessage(fmt.Sprint(margin + 1))
			return mustMarshal(object)
		}

		rec := &recorder{}
		Conformance(rec, server.Client(t), ConformanceOptions{})

		if len(rec.errors) == 0 {
			t.Fatal("набор зелен на несходящейся тройке — значит он не проверяет то, что заявляет")
		}
		found := false
		for _, message := range rec.errors {
			if strings.Contains(message, "тройка ставок не сходится") {
				found = true
			}
		}
		if !found {
			t.Errorf("набор покраснел, но НЕ по тройке ставок:\n  %s", strings.Join(rec.errors, "\n  "))
		}
	})

	// ⚠⚠ Повтор, ответивший 201, означает ВТОРУЮ конверсию — то есть вторую
	// денежную ногу. Набор обязан это поймать, и проверить это одной мутацией
	// нельзя: нужна дверь, которая так отвечает.
	t.Run("красный, когда повтор отвечает 201", func(t *testing.T) {
		server := NewServer(t)
		server.repeatStatus = http.StatusCreated

		rec := &recorder{}
		Conformance(rec, server.Client(t), ConformanceOptions{})

		found := false
		for _, message := range rec.errors {
			if strings.Contains(message, "ВТОРУЮ конверсию") {
				found = true
			}
		}
		if !found {
			t.Errorf("набор не заметил, что повтор записал вторую конверсию:\n  %s",
				strings.Join(rec.errors, "\n  "))
		}
	})

	// Пропуск выплат обязан именно ПРОПУСКАТЬ, а не молча зеленеть: пропущенное и
	// пройденное должны быть различимы в выводе.
	t.Run("SkipPayouts объявляет себя", func(t *testing.T) {
		server := NewServer(t)
		rec := &recorder{}

		Conformance(rec, server.Client(t), ConformanceOptions{SkipPayouts: true})

		if len(rec.errors) > 0 {
			t.Errorf("набор покраснел: %s", strings.Join(rec.errors, "\n  "))
		}
		skipped := false
		for _, message := range rec.logs {
			if strings.Contains(message, "пропущен по SkipPayouts") {
				skipped = true
			}
		}
		if !skipped {
			t.Error("пропуск выплат не объявлен — пропущенное неотличимо от пройденного")
		}
		for _, r := range server.Requests() {
			if r.Path == "/cpa/payouts" && r.Method == http.MethodPost {
				t.Error("заявка на выплату всё-таки ушла, хотя цепочка пропущена")
			}
		}
	})
}

// noNetwork — транспорт, который считает обращения и ни одно не выпускает в сеть.
// Тест про боевой адрес не имеет права дойти до боевого адреса даже при дефекте.
type noNetwork struct{ calls atomic.Int32 }

func (n *noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	n.calls.Add(1)
	return nil, fmt.Errorf("cpatest: сеть в этом тесте запрещена")
}

func clientWithoutNetwork(t *testing.T, opts cpa.ClientOptions) (*cpa.Client, *noNetwork) {
	t.Helper()
	transport := &noNetwork{}
	opts.HTTPClient = &http.Client{Transport: transport}
	opts.MaxRetries = cpa.Ptr(0)
	c, err := cpa.New("tk_target_probe", &opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, transport
}

// ⚠⚠ Набор не делает НИ ОДНОГО запроса, если клиент смотрит не на песочницу и
// не на стенд. Главный случай — нулевое значение Environment: это Production, и
// забытое `Environment: cpa.Sandbox` в CI интегратора иначе создавало бы в живой
// сети партнёра, одобренную конверсию на 2500 ₽ и заявку на выплату при каждом
// прогоне.
func TestConformanceRefusesAnythingButSandboxAndStand(t *testing.T) {
	cases := []struct {
		name string
		opts cpa.ClientOptions
	}{
		{"умолчание — это прод", cpa.ClientOptions{}},
		{"прод явно", cpa.ClientOptions{Environment: cpa.Production}},
		{"боевой адрес руками", cpa.ClientOptions{BaseURL: "https://api.recca.ru"}},
		{"посторонний хост", cpa.ClientOptions{BaseURL: "https://staging.recca.ru"}},
		// Совпадение по началу имени — не песочница.
		{"песочница как приставка чужого имени", cpa.ClientOptions{BaseURL: "https://api-sandbox.recca.ru.evil.example"}},
		{"песочница с путём", cpa.ClientOptions{BaseURL: "https://api-sandbox.recca.ru/proxy"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, transport := clientWithoutNetwork(t, tc.opts)
			rec := &recorder{}

			Conformance(rec, c, ConformanceOptions{})

			if got := transport.calls.Load(); got != 0 {
				t.Fatalf("набор сделал %d запрос(ов) на %s — ради этого и стоит проверка", got, c.BaseURL())
			}
			if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "отказывается работать") {
				t.Errorf("набор обязан покраснеть ОДНОЙ причиной — адресом:\n  %s", strings.Join(rec.errors, "\n  "))
			}
			if !strings.Contains(strings.Join(rec.errors, ""), c.BaseURL()) {
				t.Error("отказ не называет адрес, на который смотрит клиент")
			}
		})
	}
}

// Вторая половина: запрет доказывает разрешение. Песочница и петля обязаны
// пропускаться — иначе проверка выше была бы зелена на наборе, который не
// работает нигде.
func TestConformanceAllowsSandboxAndLoopback(t *testing.T) {
	cases := []struct {
		name string
		opts cpa.ClientOptions
	}{
		{"песочница", cpa.ClientOptions{Environment: cpa.Sandbox}},
		{"127.0.0.1", cpa.ClientOptions{BaseURL: "http://127.0.0.1:1"}},
		{"localhost", cpa.ClientOptions{BaseURL: "http://localhost:1"}},
		{"[::1]", cpa.ClientOptions{BaseURL: "http://[::1]:1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, transport := clientWithoutNetwork(t, tc.opts)
			rec := &recorder{}

			Conformance(rec, c, ConformanceOptions{})

			if transport.calls.Load() == 0 {
				t.Error("набор не сделал ни одного запроса к разрешённому адресу")
			}
			for _, message := range rec.errors {
				if strings.Contains(message, "отказывается работать") {
					t.Errorf("разрешённый адрес %s отбит: %s", c.BaseURL(), message)
				}
			}
		})
	}
}

// ⚠ Ключ клика уникален на прогон. Постоянный ключ вернул бы со второго прогона
// клик ПЕРВОГО — вертикаль проверяла бы меньше, чем заявляет.
func TestConformanceClickKeyIsUniquePerRun(t *testing.T) {
	server := NewServer(t)
	first := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	for _, now := range []time.Time{first, second} {
		rec := &recorder{}
		Conformance(rec, server.Client(t), ConformanceOptions{Now: now, SkipPayouts: true})
		if len(rec.errors) > 0 {
			t.Fatalf("прогон покраснел: %s", strings.Join(rec.errors, "\n  "))
		}
	}

	var keys []string
	for _, r := range server.Requests() {
		if r.Method != http.MethodPost || r.Path != "/cpa/clicks" {
			continue
		}
		var body struct {
			ClientClickID string `json:"client_click_id"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatalf("тело клика не разобрано: %v", err)
		}
		keys = append(keys, body.ClientClickID)
	}
	if len(keys) != 2 {
		t.Fatalf("кликов %d, ждали два", len(keys))
	}
	if keys[0] == keys[1] {
		t.Errorf("оба прогона прислали один ключ клика %q", keys[0])
	}
	for i, now := range []time.Time{first, second} {
		if want := strconv.FormatInt(now.Unix(), 10); !strings.Contains(keys[i], want) {
			t.Errorf("ключ клика %q не несёт штамп прогона %s", keys[i], want)
		}
	}
}

// errorsIs — короткая форма, чтобы не тащить errors в каждый случай.
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

// Стенд держит тот же ключ цели, что дверь: нет ключа, пустая строка и "1" —
// одна конверсия. Иначе Ptr("") давал бы на стенде 201, а в песочнице 200.
func TestServerGoalKeyFollowsDoor(t *testing.T) {
	s := NewServer(t)
	c := s.Client(t)
	ctx := context.Background()

	cases := []struct {
		name string
		goal *string
		want int
	}{
		{"без цели — создана", nil, http.StatusCreated},
		{"пустая цель — та же конверсия", cpa.Ptr(""), http.StatusOK},
		{"цель \"1\" — та же конверсия", cpa.Ptr("1"), http.StatusOK},
		{"другая цель — новая конверсия", cpa.Ptr("webinar"), http.StatusCreated},
	}
	for _, tc := range cases {
		_, resp, err := c.Conversions.Create(ctx, cpa.CreateConversionParams{
			ClickID: "clk_1", ExternalID: "order-goal", Status: "approved", Goal: tc.goal,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if resp.StatusCode != tc.want {
			t.Errorf("%s: HTTP %d, ждали %d", tc.name, resp.StatusCode, tc.want)
		}
	}
}

// ⚠⚠ Сценарий 4 проверяет СОГЛАСОВАННОСТЬ баланса и двери заявок. Четыре мира:
// два согласованных — зелёные, две лжи — красные, и красные по своей причине.
// Найдено прогоном против песочницы 24.09: у свежего партнёра всё в холде,
// can_request = false, а набор шёл в заявку вслепую и краснел ВСЕГДА — на
// правильном ответе двери.
func TestConformancePayoutChainFollowsCanRequest(t *testing.T) {
	cases := []struct {
		name          string
		canRequest    *bool
		refusePayouts bool
		wantError     string // подстрока ошибки; пусто — зелёный
		wantLog       string // подстрока журнала, обязательная при зелёном
		wantPayoutOK  bool   // заявка обязана была пройти
	}{
		{
			name:         "баланс полон, дверь принимает — положительная ветка",
			canRequest:   cpa.Ptr(true),
			wantPayoutOK: true,
		},
		{
			name:          "баланс пуст, дверь отказывает — согласовано, зелёный",
			canRequest:    cpa.Ptr(false),
			refusePayouts: true,
			wantLog:       "положительная ветка выплаты не измерена",
		},
		{
			name:       "баланс пуст, а дверь приняла — баланс врёт",
			canRequest: cpa.Ptr(false),
			wantError:  "баланс врёт",
		},
		{
			name:          "баланс обещал, а дверь отказала — врёт can_request",
			canRequest:    cpa.Ptr(true),
			refusePayouts: true,
			wantError:     "can_request врёт",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(t)
			server.canRequest = tc.canRequest
			server.refusePayouts = tc.refusePayouts
			rec := &recorder{}

			Conformance(rec, server.Client(t), ConformanceOptions{})

			if tc.wantError == "" {
				if len(rec.errors) > 0 {
					t.Fatalf("набор покраснел в согласованном мире:\n  %s", strings.Join(rec.errors, "\n  "))
				}
			} else {
				found := false
				for _, message := range rec.errors {
					if strings.Contains(message, "цепочка 4") && strings.Contains(message, tc.wantError) {
						found = true
					}
				}
				if !found {
					t.Fatalf("набор не покраснел по причине %q:\n  %s", tc.wantError, strings.Join(rec.errors, "\n  "))
				}
			}
			if tc.wantLog != "" {
				logged := false
				for _, message := range rec.logs {
					if strings.Contains(message, tc.wantLog) {
						logged = true
					}
				}
				if !logged {
					t.Errorf("неизмеренная ветка не объявлена — пропущенное неотличимо от пройденного")
				}
			}

			// Положительная ветка обязана дойти до повтора с другой суммой: две
			// заявки ушли, а не одна.
			posts := 0
			for _, r := range server.Requests() {
				if r.Path == "/cpa/payouts" && r.Method == http.MethodPost {
					posts++
				}
			}
			if tc.wantPayoutOK && posts != 2 {
				t.Errorf("заявок ушло %d, ждали 2 (заявка и повтор с другой суммой)", posts)
			}
			if !tc.wantPayoutOK && posts != 1 {
				t.Errorf("заявок ушло %d, ждали 1 — проверка согласованности одна заявка", posts)
			}
		})
	}
}

// Второй ремень Conformance: адрес — петля (стенд), но сервер называет не
// песочницу. Набор обязан остановиться после /cpa/self и не сделать НИ ОДНОЙ
// записи — ни партнёра, ни конверсии, ни заявки на выплату.
func TestConformanceStopsUnlessServerSaysSandbox(t *testing.T) {
	for _, env := range []string{"production", "staging"} {
		t.Run(env, func(t *testing.T) {
			server := NewServer(t)
			server.environment = env
			rec := &recorder{}

			Conformance(rec, server.Client(t), ConformanceOptions{})

			for _, r := range server.Requests() {
				if r.Method != http.MethodGet || r.Path != "/cpa/self" {
					t.Errorf("после среды %q набор обратился %s %s — запись в чужой среде", env, r.Method, r.Path)
				}
			}
			joined := strings.Join(rec.errors, "\n  ")
			if len(rec.errors) != 1 || !strings.Contains(joined, "остановлен до первой записи") {
				t.Errorf("набор обязан покраснеть ОДНОЙ причиной — средой:\n  %s", joined)
			}
			if !strings.Contains(joined, env) {
				t.Errorf("отказ не называет среду %q:\n  %s", env, joined)
			}
		})
	}
}

// Через стенд проходят все три состояния рекрутёра, и открепление уходит в тело
// явным null. Состояния партнёров стенд не держит, поэтому «откреплён» он
// показывает фикстурой без рекрутёра, а доказательство — записанное обращение.
func TestServerRecruiterThreeStates(t *testing.T) {
	server := NewServer(t)
	c := server.Client(t)
	ctx := context.Background()

	attached, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
		ExternalID: "blg-1", Role: cpa.PartnerWebmaster, RecruiterExternalID: "scout-1",
	})
	if err != nil {
		t.Fatalf("прикрепить: %v", err)
	}
	if attached.RecruiterExternalID == nil || *attached.RecruiterExternalID != "scout-1" {
		t.Errorf("после прикрепления рекрутёр %v", attached.RecruiterExternalID)
	}

	detached, _, err := c.Partners.Upsert(ctx, cpa.UpsertPartnerParams{
		ExternalID: "blg-1", Role: cpa.PartnerWebmaster, DetachRecruiter: true,
	})
	if err != nil {
		t.Fatalf("открепить: %v", err)
	}
	if detached.RecruiterExternalID != nil {
		t.Errorf("после открепления рекрутёр %v, ждали null", *detached.RecruiterExternalID)
	}
	if got := server.Requests()[1].BodyString(); !strings.Contains(got, `"recruiter_external_id":null`) {
		t.Errorf("в тело открепления не ушёл null: %s", got)
	}
}

// Пятый мир (26.09.2026): баланс пуст, потому что заявка за этот период уже
// принята, и повтор набора за тот же день получает её же с 200. Это не ложь
// баланса — первая редакция набора читала такой повтор как приём новой заявки
// и краснела на втором прогоне в сутки.
func TestConformancePayoutRepeatOfAcceptedIsConsistent(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	server := NewServer(t)
	server.canRequest = cpa.Ptr(false)
	c := server.Client(t)

	balance, _, err := c.Partners.Balance(context.Background(), "conformance-webmaster")
	if err != nil {
		t.Fatalf("баланс: %v", err)
	}
	amount := balance.MinPayoutRUB
	if amount <= 0 {
		amount = 1
	}
	params, err := cpa.NewPayoutParams("conformance-webmaster", amount,
		cpa.DateOf(now.AddDate(0, 0, -30)), cpa.DateOf(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, resp, err := c.Payouts.Create(context.Background(), params); err != nil || resp.StatusCode != 201 {
		t.Fatalf("первая заявка: %v", err)
	}

	rec := &recorder{}
	Conformance(rec, c, ConformanceOptions{Now: now})

	for _, message := range rec.errors {
		if strings.Contains(message, "цепочка 4") {
			t.Fatalf("повтор принятой заявки прочитан как ложь баланса: %s", message)
		}
	}
	logged := false
	for _, message := range rec.logs {
		if strings.Contains(message, "уже принята раньше") {
			logged = true
		}
	}
	if !logged {
		t.Errorf("повтор принятой заявки не назван в журнале:\n  %s", strings.Join(rec.logs, "\n  "))
	}
}
