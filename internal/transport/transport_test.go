// Контракт исполнителя запросов: повторы, паузы, разбор конверта и отладочный
// вывод.
//
// ⚠⚠ Здесь живут все четыре дефекта пробы WV-0698 — решение об ошибке по флагу
// success без HTTP-статуса, чтение тела после декодера, тело без потолка
// размера и невыделенный транспорт. Поэтому каждый из них закреплён случаем, а
// не оставлен на внимательность.
package transport_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.recca.ru/cpa/cpatypes"
	"go.recca.ru/cpa/internal/transport"
)

// recorder — стенд со счётчиком попыток и сценарием ответов по шагам.
type recorder struct {
	attempts atomic.Int32
	steps    []step
}

type step struct {
	status  int
	body    string
	headers map[string]string
}

func (rec *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := int(rec.attempts.Add(1)) - 1
		s := rec.steps[len(rec.steps)-1]
		if n < len(rec.steps) {
			s = rec.steps[n]
		}
		for k, v := range s.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	}
}

// newExec — исполнитель против стенда с подменённым ожиданием: паузы
// записываются, а не выдерживаются, иначе прогон стоил бы секунд.
func newExec(t *testing.T, srv *httptest.Server, maxRetries int, waits *[]time.Duration) *transport.Executor {
	t.Helper()
	exec, err := transport.New(transport.Options{
		BaseURL:    srv.URL,
		Key:        "tk_test_key",
		UserAgent:  "recca-cpa-go/test",
		MaxRetries: maxRetries,
		Sleep: func(ctx context.Context, d time.Duration) error {
			*waits = append(*waits, d)
			return ctx.Err()
		},
		Jitter: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}
	return exec
}

const okBody = `{"success":true,"data":{"value":7},"meta":{"request_id":"req_ok"}}`

// Повторяемая операция переживает временный сбой: два отказа 5xx и успех с
// третьей попытки. Без этого каждый перезапуск нашего бэкенда стоил бы
// интегратору потерянной конверсии.
func TestRetriesRetryableRequestOn5xx(t *testing.T) {
	rec := &recorder{steps: []step{
		{status: 500, body: `{"success":false,"error":{"code":"INTERNAL_ERROR","message":"boom"}}`},
		{status: 502, body: ``},
		{status: 200, body: okBody},
	}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 2, &waits)

	var out struct {
		Value int `json:"value"`
	}
	resp, err := exec.Do(context.Background(), transport.Request{
		Method: http.MethodGet, Path: "/cpa/self", Out: &out, Retryable: true,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := rec.attempts.Load(); got != 3 {
		t.Errorf("попыток %d, ждали 3", got)
	}
	if out.Value != 7 {
		t.Errorf("data не разобрана: %+v", out)
	}
	if resp.RequestID != "req_ok" {
		t.Errorf("RequestID = %q", resp.RequestID)
	}
}

// ⚠⚠ Неидемпотентная операция не повторяется НИКОГДА, даже на 5xx: ответ мог
// потеряться уже после того, как конверсия записана, и повтор оплатил бы её
// вторично. Решение принимает вызывающий, объявляя Retryable, — транспорт его
// не угадывает.
func TestDoesNotRetryNonIdempotentRequest(t *testing.T) {
	rec := &recorder{steps: []step{{status: 500, body: `{"success":false,"error":{"code":"INTERNAL_ERROR","message":"boom"}}`}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 3, &waits)

	_, err := exec.Do(context.Background(), transport.Request{
		Method: http.MethodPost, Path: "/cpa/conversions", Body: map[string]string{"external_id": "order-1"}, Retryable: false,
	})
	if err == nil {
		t.Fatal("500 пришёл без ошибки")
	}
	if got := rec.attempts.Load(); got != 1 {
		t.Errorf("попыток %d, ждали 1 — неидемпотентную операцию повторять нельзя", got)
	}
	if len(waits) != 0 {
		t.Errorf("ожидания %v — их быть не должно", waits)
	}
}

// Бюджет повторов конечен и равен MaxRetries: без потолка клиент, встретив
// лежащий бэкенд, бился бы в него бесконечно и добавил бы нагрузки ровно тогда,
// когда её и так слишком много.
func TestRetryBudgetIsBounded(t *testing.T) {
	rec := &recorder{steps: []step{{status: 503, body: ``}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 2, &waits)

	_, err := exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self", Retryable: true})
	if err == nil {
		t.Fatal("503 пришёл без ошибки")
	}
	if got := rec.attempts.Load(); got != 3 {
		t.Errorf("попыток %d, ждали 3 (1 + MaxRetries 2)", got)
	}
	if len(waits) != 2 {
		t.Errorf("пауз %d, ждали 2", len(waits))
	}
}

// Пауза растёт вдвое: 500 мс, затем секунда. Считается она от объявленной
// базы, а не от «сколько-нибудь»: у интегратора на этих числах строится его
// собственный таймаут.
func TestBackoffDoublesWithoutJitter(t *testing.T) {
	rec := &recorder{steps: []step{{status: 500, body: ``}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 2, &waits)
	_, _ = exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self", Retryable: true})

	want := []time.Duration{500 * time.Millisecond, time.Second}
	if len(waits) != len(want) {
		t.Fatalf("пауз %d (%v), ждали %d", len(waits), waits, len(want))
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Errorf("пауза %d = %v, ждали %v", i+1, waits[i], want[i])
		}
	}
}

// Разброс ±25 % нужен, чтобы сотня наших клиентов не пришла повторяться в одну
// миллисекунду. Проверяем границы, а не конкретное значение: случайность здесь
// и есть смысл.
func TestBackoffAppliesJitterWithinQuarter(t *testing.T) {
	rec := &recorder{steps: []step{{status: 500, body: ``}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	for _, jitter := range []float64{-1, 1} {
		waits = waits[:0]
		rec.attempts.Store(0)
		exec, err := transport.New(transport.Options{
			BaseURL: srv.URL, Key: "tk", MaxRetries: 1,
			Sleep:  func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return ctx.Err() },
			Jitter: func() float64 { return jitter },
		})
		if err != nil {
			t.Fatalf("transport.New: %v", err)
		}
		_, _ = exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self", Retryable: true})

		if len(waits) != 1 {
			t.Fatalf("пауз %d, ждали 1", len(waits))
		}
		low, high := 375*time.Millisecond, 625*time.Millisecond
		if waits[0] < low || waits[0] > high {
			t.Errorf("при jitter %v пауза %v вне [%v, %v]", jitter, waits[0], low, high)
		}
	}
}

// 429 — единственный случай, где паузу называет сервер. Берём её из
// Retry-After, а не из своей формулы: наш бюджет считает он, а не мы.
func TestRetries429UsingRetryAfter(t *testing.T) {
	rec := &recorder{steps: []step{
		{status: 429, body: ``, headers: map[string]string{"Retry-After": "2", "X-RateLimit-Remaining": "0"}},
		{status: 200, body: okBody},
	}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 2, &waits)

	resp, err := exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self", Retryable: true})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(waits) != 1 || waits[0] != 2*time.Second {
		t.Errorf("паузы %v, ждали одну в 2s из Retry-After", waits)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d", resp.StatusCode)
	}
}

// ⚠ Долгая пауза не высиживается: ждать минуту внутри чужого запроса значит
// держать его горутину и его таймаут. Возвращаем отказ с кодом сервера —
// пусть вызывающий решает сам.
func TestDoesNotRetry429BeyondCap(t *testing.T) {
	rec := &recorder{steps: []step{
		{status: 429, body: `{"success":false,"error":{"code":"RATE_LIMIT_EXCEEDED","message":"budget exhausted"}}`,
			headers: map[string]string{"Retry-After": "60"}},
	}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 3, &waits)

	_, err := exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self", Retryable: true})
	if err == nil {
		t.Fatal("429 пришёл без ошибки")
	}
	var apiErr *cpatypes.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != cpatypes.CodeRateLimitExceeded {
		t.Fatalf("ошибка = %v, ждали APIError с RATE_LIMIT_EXCEEDED", err)
	}
	if got := rec.attempts.Load(); got != 1 {
		t.Errorf("попыток %d, ждали 1 — пауза в минуту не высиживается", got)
	}
	if len(waits) != 0 {
		t.Errorf("паузы %v — ждать не собирались", waits)
	}
}

// Отмена контекста прекращает работу немедленно: следующей попытки не будет.
// Иначе отменённый пользователем запрос продолжал бы тратить его бюджет.
func TestContextCancellationStopsRetries(t *testing.T) {
	rec := &recorder{steps: []step{{status: 500, body: ``}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	exec, err := transport.New(transport.Options{
		BaseURL: srv.URL, Key: "tk", MaxRetries: 3,
		Sleep: func(ctx context.Context, d time.Duration) error {
			cancel() // отмена приходит, пока мы ждём паузу
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}

	_, err = exec.Do(ctx, transport.Request{Method: http.MethodGet, Path: "/cpa/self", Retryable: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка = %v, ждали context.Canceled", err)
	}
	if got := rec.attempts.Load(); got != 1 {
		t.Errorf("попыток %d, ждали 1 — после отмены новых запросов быть не должно", got)
	}
}

// ⚠ Тот же инвариант БЕЗ тестового крючка: настоящее ожидание обязано быть
// отменяемым. Иначе time.Sleep продержал бы горутину полсекунды после того,
// как ответ уже никому не нужен, и тест с крючком этого бы не увидел.
func TestRealWaitIsInterruptible(t *testing.T) {
	rec := &recorder{steps: []step{{status: 500, body: ``}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	exec, err := transport.New(transport.Options{BaseURL: srv.URL, Key: "tk", MaxRetries: 3})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err = exec.Do(ctx, transport.Request{Method: http.MethodGet, Path: "/cpa/self", Retryable: true})
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("отказ 500 с истёкшим контекстом пришёл без ошибки")
	}
	if elapsed > 400*time.Millisecond {
		t.Errorf("Do вернулся через %v — ожидание не прерывается контекстом", elapsed)
	}
}

// Запрос, отменённый ДО отправки, в сеть не уходит вовсе.
func TestAlreadyCancelledContextSendsNothing(t *testing.T) {
	rec := &recorder{steps: []step{{status: 200, body: okBody}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 1, &waits)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := exec.Do(ctx, transport.Request{Method: http.MethodGet, Path: "/cpa/self", Retryable: true}); err == nil {
		t.Fatal("запрос с отменённым контекстом прошёл")
	}
	if got := rec.attempts.Load(); got != 0 {
		t.Errorf("запросов %d, ждали 0", got)
	}
}

// ⚠⚠ Дефект пробы WV-0698: решение об отказе принималось по флагу success без
// HTTP-статуса. Классов два, и различаются они тем, ЧТО сообщает отказ.
func TestStatusAndEnvelopeAreSeparateFailureClasses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode cpatypes.ErrorCode
	}{
		{
			name:     "не-2xx с кодом сервера",
			status:   http.StatusForbidden,
			body:     `{"success":false,"error":{"code":"PRODUCT_PAUSED","message":"paused"},"meta":{"request_id":"req_1"}}`,
			wantCode: cpatypes.CodeProductPaused,
		},
		{
			name:     "не-2xx без тела вовсе",
			status:   http.StatusBadGateway,
			body:     ``,
			wantCode: cpatypes.CodeInternalError,
		},
		{
			// ⚠⚠ Вторая сторона гейта, и без неё набор зелен у реализации,
			// которая смотрит ТОЛЬКО на флаг конверта: статус 404 сообщает об
			// отказе, а success:true уговаривает считать ответ удачным.
			// Проверено мутацией — без этого случая подмена условия статуса на
			// «всегда ложь» проходила незамеченной.
			name:     "не-2xx, а конверт говорит об успехе",
			status:   http.StatusNotFound,
			body:     `{"success":true,"data":{"value":1},"meta":{"request_id":"req_4"}}`,
			wantCode: cpatypes.CodeMalformedResponse,
		},
		{
			name:     "2xx с success:false и кодом",
			status:   http.StatusOK,
			body:     `{"success":false,"error":{"code":"WINDOW_EXPIRED","message":"late"},"meta":{"request_id":"req_2"}}`,
			wantCode: cpatypes.CodeWindowExpired,
		},
		{
			// Кода нет, а конверт говорит «не вышло»: статус 200 об отказе не
			// сообщает ничего, поэтому код нужен свой. HTTP_200 в журнале
			// потребителя читался бы как успех.
			name:     "2xx с success:false без кода",
			status:   http.StatusOK,
			body:     `{"success":false,"meta":{"request_id":"req_3"}}`,
			wantCode: cpatypes.CodeMalformedResponse,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{steps: []step{{status: tc.status, body: tc.body}}}
			srv := httptest.NewServer(rec.handler())
			defer srv.Close()

			var waits []time.Duration
			exec := newExec(t, srv, 0, &waits)

			_, err := exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self"})
			if err == nil {
				t.Fatalf("статус %d, тело %q — ошибки нет", tc.status, tc.body)
			}
			var apiErr *cpatypes.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("ошибка не *APIError: %T", err)
			}
			if apiErr.Code != tc.wantCode {
				t.Errorf("Code = %q, ждали %q", apiErr.Code, tc.wantCode)
			}
			if apiErr.HTTPStatus != tc.status {
				t.Errorf("HTTPStatus = %d, ждали %d", apiErr.HTTPStatus, tc.status)
			}
		})
	}
}

// ⚠ Два отказа формируются до того, как запрос дойдёт до метода, и тела с meta
// у них нет: «ключ не предъявлен» и «исчерпан бюджет». Идентификатор там всё
// равно есть — в заголовке, который ставится раньше всего прочего.
func TestRequestIDComesFromHeaderWhenBodyHasNone(t *testing.T) {
	rec := &recorder{steps: []step{
		{status: 401, body: ``, headers: map[string]string{"X-Request-Id": "req_from_header"}},
	}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 0, &waits)

	_, err := exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self"})
	var apiErr *cpatypes.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ошибка не *APIError: %v", err)
	}
	if got, want := apiErr.RequestID, "req_from_header"; got != want {
		t.Errorf("RequestID = %q, ждали %q", got, want)
	}
}

// Заголовок приоритетнее тела: контракт прямо велит читать его оттуда, потому
// что он есть в любом ответе, включая те, что формирует не код Recca.
func TestRequestIDHeaderWinsOverMeta(t *testing.T) {
	rec := &recorder{steps: []step{
		{status: 200, body: `{"success":true,"data":{},"meta":{"request_id":"req_from_meta"}}`,
			headers: map[string]string{"X-Request-Id": "req_from_header"}},
	}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 0, &waits)

	resp, err := exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got, want := resp.RequestID, "req_from_header"; got != want {
		t.Errorf("RequestID = %q, ждали %q", got, want)
	}
	if got, want := resp.Meta.RequestID, "req_from_meta"; got != want {
		t.Errorf("Meta.RequestID = %q, ждали %q — тело обязано доезжать как есть", got, want)
	}
}

// Наследие LOOP-01: успех без данных — не пустой ответ, а нарушение контракта.
// json.Unmarshal оставил бы структуру нулевой, и потребитель увидел бы суммы,
// которых дверь не присылала.
func TestSuccessWithoutDataIsRefused(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"data нет вовсе", `{"success":true,"meta":{"request_id":"req_1"}}`},
		{"data равна null", `{"success":true,"data":null,"meta":{"request_id":"req_1"}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{steps: []step{{status: 200, body: tc.body}}}
			srv := httptest.NewServer(rec.handler())
			defer srv.Close()

			var waits []time.Duration
			exec := newExec(t, srv, 0, &waits)

			var out struct {
				Value int `json:"value"`
			}
			_, err := exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self", Out: &out})
			if err == nil {
				t.Fatal("ответ без данных принят как успешный")
			}
			var apiErr *cpatypes.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != cpatypes.CodeMalformedResponse {
				t.Errorf("ошибка = %v, ждали MALFORMED_RESPONSE", err)
			}
		})
	}

	// ⚠ Запрет обязан доказать разрешение: тот же ответ без Out — законен.
	// Метод, которому данные не нужны, не должен падать из-за их отсутствия.
	rec := &recorder{steps: []step{{status: 200, body: `{"success":true,"meta":{"request_id":"req_1"}}`}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var waits []time.Duration
	exec := newExec(t, srv, 0, &waits)
	if _, err := exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self"}); err != nil {
		t.Errorf("ответ без data и без Out отвергнут: %v", err)
	}
}

// ⚠⚠ Дефект пробы WV-0698: тело читалось без потолка. Сервер, ответивший
// гигабайтом, обязан дать ошибку, а не съесть память процесса интегратора.
func TestResponseBodyIsCapped(t *testing.T) {
	huge := `{"success":true,"data":{"filler":"` + strings.Repeat("x", 4096) + `"}}`
	rec := &recorder{steps: []step{{status: 200, body: huge}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	exec, err := transport.New(transport.Options{
		BaseURL: srv.URL, Key: "tk", MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}

	_, err = exec.Do(context.Background(), transport.Request{Method: http.MethodGet, Path: "/cpa/self"})
	if err == nil {
		t.Fatal("тело сверх потолка принято без ошибки")
	}
	if !strings.Contains(err.Error(), "1024") && !strings.Contains(err.Error(), "потол") {
		t.Errorf("ошибка не называет предел: %v", err)
	}
}

// Отладочный вывод существует ради диагностики и не имеет права стать утечкой:
// тела нет никогда, ключ арендатора маскируется даже при LogHeaders.
func TestDebugLogCarriesNoBodyAndMasksKey(t *testing.T) {
	rec := &recorder{steps: []step{{status: 200, body: `{"success":true,"data":{"secret_value":"пароль-в-ответе"},"meta":{"request_id":"req_1"}}`}}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	exec, err := transport.New(transport.Options{
		BaseURL: srv.URL, Key: "tk_super_secret_key", Logger: logger, LogHeaders: true,
	})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}
	if _, err := exec.Do(context.Background(), transport.Request{
		Method: http.MethodPost, Path: "/cpa/conversions",
		Body: map[string]string{"external_id": "order-секрет"},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("логгер задан, а записей нет — отладка не работает вовсе")
	}
	for _, forbidden := range []string{"tk_super_secret_key", "пароль-в-ответе", "order-секрет"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("в отладочном выводе есть %q:\n%s", forbidden, out)
		}
	}
	for _, want := range []string{"/federation/v1/cpa/conversions", "POST"} {
		if !strings.Contains(out, want) {
			t.Errorf("в отладочном выводе нет %q:\n%s", want, out)
		}
	}
}

// Сетевая ошибка (соединение не установилось) для повторяемой операции —
// такой же повод попробовать ещё раз, как и 5xx: до сервера запрос не дошёл.
func TestRetriesOnNetworkError(t *testing.T) {
	var calls atomic.Int32
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, fmt.Errorf("соединение оборвано")
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(okBody)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	})

	var waits []time.Duration
	exec, err := transport.New(transport.Options{
		BaseURL:    "https://api-sandbox.recca.ru",
		Key:        "tk",
		MaxRetries: 2,
		HTTPClient: &http.Client{Transport: rt},
		Sleep:      func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return ctx.Err() },
	})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}

	if _, err := exec.Do(context.Background(), transport.Request{
		Method: http.MethodGet, Path: "/cpa/self", Retryable: true,
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("обращений %d, ждали 2", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
