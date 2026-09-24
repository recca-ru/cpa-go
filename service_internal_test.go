// Контракт общего слоя сервисов: политика повтора, полнота страницы и то, что
// написано в ошибке разбора.
//
// ⚠ Тест внутренний (package cpa), потому что предмет — неэкспортируемые do и
// doList. Снаружи их не видно, а проверять их через один только
// Conversions.Create значило бы доказывать общий слой одним его частным
// случаем: у Create путь один, метод один и идемпотентность объявлена.
package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.recca.ru/cpa/cpatypes"
)

// row — строка страницы. Локальный тип, а не Conversion: здесь проверяется
// механика полноты, и фикстура из двенадцати полей контракта прятала бы предмет.
type row struct {
	ID  string `json:"id" cpa:"required"`
	Sum int    `json:"sum_rub" cpa:"required"`
}

// stand — дверь-заглушка: считает попытки, записывает увиденное и отвечает по
// сценарию (последний шаг повторяется, если попыток больше).
type stand struct {
	attempts atomic.Int32
	methods  []string
	paths    []string
	queries  []string
	steps    []step
}

type step struct {
	status int
	body   string
}

func (s *stand) start(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.attempts.Add(1)) - 1
		s.methods = append(s.methods, r.Method)
		s.paths = append(s.paths, r.URL.Path)
		s.queries = append(s.queries, r.URL.RawQuery)

		st := s.steps[len(s.steps)-1]
		if n < len(s.steps) {
			st = s.steps[n]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st.status)
		_, _ = w.Write([]byte(st.body))
	}))
	t.Cleanup(srv.Close)

	c, err := New("tk_test_key", &ClientOptions{BaseURL: srv.URL, MaxRetries: Ptr(1)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func envelope(data string) string {
	return `{"success":true,"data":` + data + `,"meta":{"request_id":"req_1"}}`
}

const failure = `{"success":false,"error":{"code":"INTERNAL_ERROR","message":"boom"}}`

// ⚠⚠ Политика повтора — по ОПЕРАЦИИ, а не по методу, и это главное решение
// общего слоя. Чтение повторяется всегда: ничего не менялось, повторить нечем
// навредить. Запись — только объявившая себя идемпотентной, потому что ответ мог
// потеряться уже ПОСЛЕ того, как конверсия записана, и слепой повтор оплатил бы
// её вторично.
//
// Асимметрия умолчания проверяется прямо: забыть объявление у записи можно, и
// цена этого — потерянные повторы, а не второй платёж.
func TestRetryPolicyIsPerOperation(t *testing.T) {
	cases := []struct {
		name        string
		req         request
		wantAttempt int32
	}{
		{
			name:        "чтение повторяется, объявлять нечего",
			req:         request{Method: http.MethodGet, Path: "/cpa/offers", Subject: "оффер"},
			wantAttempt: 2,
		},
		{
			name:        "запись без объявления НЕ повторяется",
			req:         request{Method: http.MethodPost, Path: "/cpa/clicks", Subject: "переход"},
			wantAttempt: 1,
		},
		{
			name: "запись с объявлением повторяется",
			req: request{
				Method: http.MethodPost, Path: "/cpa/conversions",
				Subject: "конверсия", IdempotentWrite: true,
			},
			wantAttempt: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &stand{steps: []step{
				{status: http.StatusServiceUnavailable, body: failure},
				{status: http.StatusOK, body: envelope(`{"id":"x","sum_rub":1}`)},
			}}
			c := s.start(t)

			_, _, _ = do[row](context.Background(), &c.common, tc.req)

			if got := s.attempts.Load(); got != tc.wantAttempt {
				t.Errorf("попыток %d, ждали %d", got, tc.wantAttempt)
			}
		})
	}
}

// ⚠⚠ Client.Do — открытая дверь «для того, чего ещё нет в сервисах», и она не
// повторяет запись НИКОГДА: вызывающий не сообщил ей, идемпотентна ли операция,
// а угадывать здесь значит оплатить чужую конверсию вторично. Документация
// метода это обещала с ST-2, но доказательства у обещания не было.
func TestClientDoNeverRetriesWrites(t *testing.T) {
	cases := []struct {
		method      string
		wantAttempt int32
	}{
		{http.MethodGet, 2},
		{http.MethodPost, 1},
		{http.MethodDelete, 1},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			s := &stand{steps: []step{
				{status: http.StatusServiceUnavailable, body: failure},
				{status: http.StatusOK, body: envelope(`{"id":"x","sum_rub":1}`)},
			}}
			c := s.start(t)

			_, _ = c.Do(context.Background(), tc.method, "/cpa/whatever", nil, nil)

			if got := s.attempts.Load(); got != tc.wantAttempt {
				t.Errorf("попыток %d, ждали %d", got, tc.wantAttempt)
			}
		})
	}
}

// Страница: полный набор полей обязана нести КАЖДАЯ строка.
//
// ⚠⚠ Неполна здесь ТРЕТЬЯ строка намеренно. Проверка первой строки была бы
// зелена на этих же данных и выглядела бы строгим разбором — а усечённая строка
// доехала бы до интерфейса нулями, то есть выдуманными суммами.
func TestDoListChecksEveryRow(t *testing.T) {
	s := &stand{steps: []step{{status: http.StatusOK, body: envelope(
		`[{"id":"a","sum_rub":1},{"id":"b","sum_rub":2},{"id":"c"}]`,
	)}}}
	c := s.start(t)

	page, _, err := doList[row](context.Background(), &c.common, request{
		Method: http.MethodGet, Path: "/cpa/conversions", Subject: "страница конверсий",
	})
	if err == nil {
		t.Fatalf("страница с усечённой строкой принята: %+v", page)
	}
	if !errors.Is(err, cpatypes.ErrIncompleteData) {
		t.Errorf("ошибка = %v, ждали ErrIncompleteData", err)
	}
	if !strings.Contains(err.Error(), "sum_rub") {
		t.Errorf("ошибка не называет недостающее поле: %v", err)
	}
	// Номер строки нужен затем же, зачем request_id: без него у двери с сотней
	// строк непонятно, какую смотреть.
	if !strings.Contains(err.Error(), "строка 2") {
		t.Errorf("ошибка не называет номер строки: %v", err)
	}
}

// ⚠⚠ «Строк нет» и «данных не прислали» — разные ответы, и различать их обязан
// клиент: пустая страница законна, а null на её месте означает, что дверь
// обещания не выполнила. Сложи их — и «ничего не нашлось» станет неотличимо от
// поломки.
func TestDoListSeparatesEmptyPageFromMissingData(t *testing.T) {
	t.Run("пустая страница — успех", func(t *testing.T) {
		s := &stand{steps: []step{{status: http.StatusOK, body: envelope(`[]`)}}}
		c := s.start(t)

		page, _, err := doList[row](context.Background(), &c.common, request{
			Method: http.MethodGet, Path: "/cpa/conversions", Subject: "страница конверсий",
		})
		if err != nil {
			t.Fatalf("пустая страница отвергнута: %v", err)
		}
		if len(page) != 0 {
			t.Errorf("строк %d, ждали 0", len(page))
		}
	})

	// ⚠ Отказ приходит от ТРАНСПОРТА, а не от проверки полей: «успешный ответ без
	// data» решается один раз и для всех методов сразу, включая Client.Do. Своя
	// ветка на этот случай в doList была бы второй копией решения — и, что хуже,
	// копией недостижимой: сюда исполнение уже не доходит.
	t.Run("data = null — отказ, и его даёт транспорт", func(t *testing.T) {
		s := &stand{steps: []step{{status: http.StatusOK, body: envelope(`null`)}}}
		c := s.start(t)

		_, _, err := doList[row](context.Background(), &c.common, request{
			Method: http.MethodGet, Path: "/cpa/conversions", Subject: "страница конверсий",
		})
		if !errors.Is(err, ErrMalformedResponse) {
			t.Errorf("ошибка = %v, ждали ErrMalformedResponse", err)
		}
		if errors.Is(err, cpatypes.ErrIncompleteData) {
			t.Error("это ответ не по контракту, а не неполные данные: путать их значит искать дефект не там")
		}
	})
}

// Страница разбирается целиком, а пагинация достаётся из meta: у страницы это
// часть ОТВЕТА, а не данных, и складывать их в один тип значило бы заставить
// каждый метод списка объявлять свою обёртку.
func TestDoListReturnsRowsAndPageMeta(t *testing.T) {
	s := &stand{steps: []step{{status: http.StatusOK, body: `{"success":true,"data":` +
		`[{"id":"a","sum_rub":1},{"id":"b","sum_rub":2}],` +
		`"meta":{"request_id":"req_page","total":57,"count":2,"offset":0,"limit":2,"has_more":true,"truncated":false}}`}}}
	c := s.start(t)

	page, resp, err := doList[row](context.Background(), &c.common, request{
		Method: http.MethodGet, Path: "/cpa/conversions", Subject: "страница конверсий",
	})
	if err != nil {
		t.Fatalf("doList: %v", err)
	}
	if len(page) != 2 || page[1].ID != "b" || page[1].Sum != 2 {
		t.Fatalf("страница разобрана неверно: %+v", page)
	}
	if resp.Meta.Total == nil || *resp.Meta.Total != 57 {
		t.Errorf("total = %v, ждали 57 — без него неизвестно, есть ли ещё страницы", resp.Meta.Total)
	}
	if resp.Meta.HasMore == nil || !*resp.Meta.HasMore {
		t.Errorf("has_more = %v", resp.Meta.HasMore)
	}
	// ⚠ Truncated пришёл false — и это УТВЕРЖДЕНИЕ «мы посмотрели всё», а не
	// отсутствие оговорки. Потерять его значит выдать неполную выдачу за полную.
	if resp.Meta.Truncated == nil {
		t.Error("truncated потерян: false и «не прислали» — разные ответы")
	}
}

// Параметры строки запроса доезжают до двери. Без них Offers.List не сможет
// назвать партнёра, а список конверсий — период.
func TestRequestCarriesQuery(t *testing.T) {
	s := &stand{steps: []step{{status: http.StatusOK, body: envelope(`[]`)}}}
	c := s.start(t)

	_, _, err := doList[row](context.Background(), &c.common, request{
		Method:  http.MethodGet,
		Path:    "/cpa/conversions",
		Query:   map[string][]string{"limit": {"20"}, "partner_external_id": {"blogger-42"}},
		Subject: "страница конверсий",
	})
	if err != nil {
		t.Fatalf("doList: %v", err)
	}
	if len(s.queries) != 1 {
		t.Fatalf("попыток %d", len(s.queries))
	}
	for _, want := range []string{"limit=20", "partner_external_id=blogger-42"} {
		if !strings.Contains(s.queries[0], want) {
			t.Errorf("в строке запроса нет %q: %q", want, s.queries[0])
		}
	}
}

// Ошибка разбора называет предмет и request_id: по первому интегратор понимает,
// что сломалось, по второму мы находим цепочку у себя.
func TestParseErrorNamesSubjectAndRequestID(t *testing.T) {
	t.Run("предмет назван", func(t *testing.T) {
		s := &stand{steps: []step{{status: http.StatusOK, body: envelope(`{"id":"a"}`)}}}
		c := s.start(t)

		_, _, err := do[row](context.Background(), &c.common, request{
			Method: http.MethodGet, Path: "/cpa/conversions/x", Subject: "конверсия",
		})
		if err == nil {
			t.Fatal("неполный ответ принят")
		}
		for _, want := range []string{"конверсия", "req_1"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("в ошибке нет %q: %v", want, err)
			}
		}
	})

	// Предмет забыли — сообщение обязано остаться осмысленным: путь называет
	// операцию не так удобно, но однозначно.
	t.Run("предмет забыли — остаётся путь", func(t *testing.T) {
		s := &stand{steps: []step{{status: http.StatusOK, body: envelope(`{"id":"a"}`)}}}
		c := s.start(t)

		_, _, err := do[row](context.Background(), &c.common, request{
			Method: http.MethodGet, Path: "/cpa/conversions/x",
		})
		if err == nil {
			t.Fatal("неполный ответ принят")
		}
		if !strings.Contains(err.Error(), "/cpa/conversions/x") {
			t.Errorf("в ошибке нет ни предмета, ни пути: %v", err)
		}
	})
}

// Все сервисы клиента смотрят в ОДНУ общую структуру, а не каждый в свою копию.
// Разойтись копиям нечем помешать, а разойдясь, они дадут сервис, ходящий не тем
// клиентом, — и увидеть это можно будет только по журналам двери.
func TestServicesShareOneCommon(t *testing.T) {
	c, err := New("tk_test_key", &ClientOptions{BaseURL: "https://stand.example"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if (*service)(c.Conversions) != &c.common {
		t.Error("Conversions смотрит не в общую структуру клиента")
	}
	if c.common.client != c {
		t.Error("общая структура не знает своего клиента")
	}
}

// Ответ двери разбирается в T целиком — проверка полноты не подменяет разбор.
func TestDoParsesValue(t *testing.T) {
	s := &stand{steps: []step{{status: http.StatusOK, body: envelope(`{"id":"a","sum_rub":1500}`)}}}
	c := s.start(t)

	got, resp, err := do[row](context.Background(), &c.common, request{
		Method: http.MethodGet, Path: "/cpa/conversions/a", Subject: "конверсия",
	})
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if got.ID != "a" || got.Sum != 1500 {
		t.Errorf("разобрано %+v", got)
	}
	if resp.RequestID != "req_1" {
		t.Errorf("request_id = %q", resp.RequestID)
	}
	if s.methods[0] != http.MethodGet || s.paths[0] != "/federation/v1/cpa/conversions/a" {
		t.Errorf("ушло %s %s", s.methods[0], s.paths[0])
	}
}

// Отказ двери доезжает до вызывающего как APIError и НЕ подменяется ошибкой
// разбора: 409 на приёме конверсии — это ответ, а не поломка формата.
func TestDoSurfacesDoorError(t *testing.T) {
	s := &stand{steps: []step{{status: http.StatusConflict, body: `{"success":false,` +
		`"error":{"code":"IDEMPOTENT_MISMATCH","message":"параметры не совпали"},` +
		`"meta":{"request_id":"req_409"}}`}}}
	c := s.start(t)

	_, resp, err := do[row](context.Background(), &c.common, request{
		Method: http.MethodPost, Path: "/cpa/conversions", Subject: "конверсия", IdempotentWrite: true,
	})
	if !errors.Is(err, ErrIdempotentMismatch) {
		t.Fatalf("ошибка = %v, ждали ErrIdempotentMismatch", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RequestID != "req_409" {
		t.Errorf("отказ двери потерял request_id: %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Errorf("ответ потерян: %+v", resp)
	}
	// ⚠ Отказ двери не повторяется: 409 — это ответ, а не сбой.
	if got := s.attempts.Load(); got != 1 {
		t.Errorf("попыток %d, ждали 1", got)
	}
}

// Страховка от подмены предмета проверки: фикстура строки обязана быть
// проверяемой в принципе. Тип без cpa:"required" дал бы зелёный на любых данных.
func TestRowFixtureIsCheckable(t *testing.T) {
	if err := cpatypes.RequireFields[row]([]byte(`{}`)); err == nil {
		t.Fatal("тип row проверке не поддаётся — все тесты выше зелены по пустоте")
	}
	var probe row
	if err := json.Unmarshal([]byte(`{"id":"a","sum_rub":1}`), &probe); err != nil {
		t.Fatalf("фикстура не разбирается: %v", err)
	}
}
