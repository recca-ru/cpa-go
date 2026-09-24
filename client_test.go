// Контракт клиента CPA-API: как он создаётся, куда обращается и что кладёт в
// заголовки. Пишется по docs/federation/cpa-api.md (разделы 2, 4), а не по
// коду платформы.
//
// ⚠ Тесты внешние (package cpa_test): всё, что здесь названо, обязано быть
// доступно потребителю. Проверка приватных деталей закрепила бы реализацию, а
// не контракт.
package cpa_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"go.recca.ru/cpa"
)

// okEnvelope — минимальный успешный ответ двери: конверт с data и meta.
const okEnvelope = `{"success":true,"data":{"ok":true},"meta":{"request_id":"req_01K5TEST"}}`

// newStub поднимает стенд, записывает последний запрос и отвечает телом body.
func newStub(t *testing.T, status int, body string, seen *http.Request) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			clone := r.Clone(context.Background())
			// Тело стенду не нужно, а читать его после ответа уже нельзя.
			clone.Body = io.NopCloser(strings.NewReader(""))
			*seen = *clone
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Ключ — единственное, без чего дверь не открывается вовсе. Пустая и
// пробельная строка отбиваются ДО сети: запрос без ключа получил бы 401, то
// есть потребитель узнал бы об опечатке в переменной окружения по чужому коду
// ошибки.
func TestNewRejectsKeylessClient(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"пустая строка", ""},
		{"пробелы", "   "},
		{"табуляция и перевод строки", "\t\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := cpa.New(tc.key, nil)
			if err == nil {
				t.Fatalf("New(%q) ошибки не вернул, клиент = %v", tc.key, c)
			}
			if c != nil {
				t.Errorf("New(%q) вернул клиент вместе с ошибкой: %v", tc.key, c)
			}
		})
	}
}

// Умолчания: без опций клиент обращается в продовую дверь и представляется
// именем SDK с версией. Имя нужно нам самим — по нему в журналах прода видно,
// какая версия клиента ходит.
func TestNewWithoutOptionsUsesProductionDefaults(t *testing.T) {
	c, err := cpa.New("tk_live", nil)
	if err != nil {
		t.Fatalf("New без опций: %v", err)
	}
	if got, want := c.BaseURL(), "https://api.recca.ru"; got != want {
		t.Errorf("BaseURL = %q, ждали %q", got, want)
	}
	if got, want := c.UserAgent(), "recca-cpa-go/"+cpa.Version; got != want {
		t.Errorf("UserAgent = %q, ждали %q", got, want)
	}
	if cpa.Version == "" {
		t.Error("cpa.Version пуст — представляться нечем")
	}
}

// Среда называет дверь. Значений ровно два, и неизвестное — ошибка, а не
// молчаливый прод: опечатка в конфигурации не должна отправить приёмочный
// прогон на боевые деньги.
func TestEnvironmentPicksBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		env     cpa.Environment
		want    string
		wantErr bool
	}{
		{"прод", cpa.Production, "https://api.recca.ru", false},
		{"песочница", cpa.Sandbox, "https://api-sandbox.recca.ru", false},
		{"неизвестная среда", cpa.Environment("staging"), "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := cpa.New("tk_live", &cpa.ClientOptions{Environment: tc.env})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("среда %q принята, а не должна", tc.env)
				}
				return
			}
			if err != nil {
				t.Fatalf("среда %q: %v", tc.env, err)
			}
			if got := c.BaseURL(); got != tc.want {
				t.Errorf("BaseURL = %q, ждали %q", got, tc.want)
			}
		})
	}
}

// Явный BaseURL — это стенд и песочница интегратора; он сильнее среды. Хвостовой
// слеш снимается: иначе путь склеился бы с двойным разделителем, и роут не
// нашёлся бы по причине, которую в журнале не видно.
func TestBaseURLOverridesEnvironmentAndTrimsSlash(t *testing.T) {
	c, err := cpa.New("tk_live", &cpa.ClientOptions{
		Environment: cpa.Production,
		BaseURL:     "https://stand.example/",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := c.BaseURL(), "https://stand.example"; got != want {
		t.Errorf("BaseURL = %q, ждали %q", got, want)
	}
}

// Отрицательное число повторов — не «поменьше», а бессмыслица. Отбиваем при
// создании: иначе она всплыла бы на первой же сетевой ошибке в проде.
func TestNewRejectsNegativeRetries(t *testing.T) {
	if _, err := cpa.New("tk_live", &cpa.ClientOptions{MaxRetries: cpa.Ptr(-1)}); err == nil {
		t.Fatal("MaxRetries = -1 принят")
	}
	if _, err := cpa.New("tk_live", &cpa.ClientOptions{MaxRetries: cpa.Ptr(0)}); err != nil {
		t.Fatalf("MaxRetries = 0 (повторов нет) обязан приниматься: %v", err)
	}
}

// Клиент неизменяем после создания. Первая половина инварианта: опции
// копируются, и правка структуры ПОСЛЕ New на клиента не действует — иначе
// один и тот же объект вёл бы себя по-разному в зависимости от того, кто ещё
// держит ссылку на опции.
func TestClientIgnoresOptionMutationAfterNew(t *testing.T) {
	opts := &cpa.ClientOptions{BaseURL: "https://stand.example", UserAgent: "acme/1.0"}
	c, err := cpa.New("tk_live", opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	opts.BaseURL = "https://evil.example"
	opts.UserAgent = "подменили"

	if got, want := c.BaseURL(), "https://stand.example"; got != want {
		t.Errorf("BaseURL = %q — клиент прочитал опции ПОСЛЕ создания, ждали %q", got, want)
	}
	if got, want := c.UserAgent(), "acme/1.0"; got != want {
		t.Errorf("UserAgent = %q, ждали %q", got, want)
	}
}

// Вторая половина инварианта: у клиента нет сеттеров. Ключ, среда и транспорт
// задаются один раз — иначе горутина, меняющая ключ на лету, порождала бы
// гонку, которой не видно ни в одном тесте потребителя.
func TestClientExposesNoSetters(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(&cpa.Client{})
	for i := range typ.NumMethod() {
		name := typ.Method(i).Name
		if strings.HasPrefix(name, "Set") {
			t.Errorf("у клиента есть метод %s — клиент обязан быть неизменяемым", name)
		}
	}
}

// Аутентификация — заголовок X-Recca-Tenant-Key (раздел 2 контракта), и ничто
// иное: Bearer здесь не используется, а ключ в строке запроса запрещён — она
// целиком попадает в журналы nginx и в Referer.
func TestDoSendsTenantKeyAndUserAgent(t *testing.T) {
	var seen http.Request
	srv := newStub(t, http.StatusOK, okEnvelope, &seen)

	c, err := cpa.New("tk_secret_value", &cpa.ClientOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Do(context.Background(), http.MethodGet, "/cpa/self", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if got, want := seen.Header.Get("X-Recca-Tenant-Key"), "tk_secret_value"; got != want {
		t.Errorf("X-Recca-Tenant-Key = %q, ждали %q", got, want)
	}
	if got, want := seen.Header.Get("User-Agent"), "recca-cpa-go/"+cpa.Version; got != want {
		t.Errorf("User-Agent = %q, ждали %q", got, want)
	}
	if got := seen.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q — дверь ключ арендатора принимает СВОИМ заголовком", got)
	}
	if got := seen.URL.RawQuery; strings.Contains(got, "tk_secret_value") {
		t.Errorf("ключ уехал в строке запроса: %q", got)
	}
}

// Версия двери — часть пути, а не забота вызывающего. Метод называет
// /cpa/self, в сеть уходит /federation/v1/cpa/self: иначе каждый из
// восемнадцати методов повторял бы префикс, и волна смены версии правила бы
// восемнадцать мест.
func TestDoPrefixesFederationVersion(t *testing.T) {
	var seen http.Request
	srv := newStub(t, http.StatusOK, okEnvelope, &seen)

	c, _ := cpa.New("tk_live", &cpa.ClientOptions{BaseURL: srv.URL})
	if _, err := c.Do(context.Background(), http.MethodGet, "/cpa/self", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got, want := seen.URL.Path, "/federation/v1/cpa/self"; got != want {
		t.Errorf("путь = %q, ждали %q", got, want)
	}
}

// Путь без ведущего слеша — ошибка вызывающего, и ловится она ДО сети: иначе
// адрес склеился бы в https://api.recca.rucpa/self и ушёл бы неизвестно куда.
func TestDoRejectsMalformedPath(t *testing.T) {
	c, _ := cpa.New("tk_live", nil)
	if _, err := c.Do(context.Background(), http.MethodGet, "cpa/self", nil, nil); err == nil {
		t.Fatal("путь без ведущего слеша принят")
	}
}

// Конверт разбирается по разделу 4: data уезжает в out, request_id — в ответ.
// Идентификатор нужен потребителю в тикете, поэтому он поле ответа, а не
// строка в журнале SDK.
func TestDoDecodesDataAndRequestID(t *testing.T) {
	srv := newStub(t, http.StatusOK, okEnvelope, nil)
	c, _ := cpa.New("tk_live", &cpa.ClientOptions{BaseURL: srv.URL})

	var out struct {
		OK bool `json:"ok"`
	}
	resp, err := c.Do(context.Background(), http.MethodGet, "/cpa/self", nil, &out)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !out.OK {
		t.Error("data не разобрана в out")
	}
	if got, want := resp.RequestID, "req_01K5TEST"; got != want {
		t.Errorf("RequestID = %q, ждали %q", got, want)
	}
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("StatusCode = %d, ждали %d", got, want)
	}
	if resp.Header == nil {
		t.Error("Header пуст — заголовки ответа (Retry-After, X-RateLimit-*) потребителю нужны")
	}
}

// Отказ приезжает типизированной ошибкой: код из конверта, статус, идентификатор
// запроса. Ветвление — по коду через errors.Is, а не по тексту сообщения:
// текст меняется без предупреждения и частью контракта не является (раздел 4).
func TestDoSurfacesAPIError(t *testing.T) {
	const body = `{"success":false,"error":{"code":"IDEMPOTENT_MISMATCH","message":"amount differs"},"meta":{"request_id":"req_01K5FAIL"}}`
	srv := newStub(t, http.StatusConflict, body, nil)
	c, _ := cpa.New("tk_live", &cpa.ClientOptions{BaseURL: srv.URL})

	_, err := c.Do(context.Background(), http.MethodPost, "/cpa/conversions", map[string]string{"external_id": "order-1"}, nil)
	if err == nil {
		t.Fatal("409 пришёл без ошибки")
	}

	var apiErr *cpa.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ошибка не *cpa.APIError: %T (%v)", err, err)
	}
	if got, want := apiErr.Code, cpa.CodeIdempotentMismatch; got != want {
		t.Errorf("Code = %q, ждали %q", got, want)
	}
	if got, want := apiErr.HTTPStatus, http.StatusConflict; got != want {
		t.Errorf("HTTPStatus = %d, ждали %d", got, want)
	}
	if got, want := apiErr.RequestID, "req_01K5FAIL"; got != want {
		t.Errorf("RequestID = %q, ждали %q", got, want)
	}
	if !errors.Is(err, cpa.ErrIdempotentMismatch) {
		t.Error("errors.Is не нашёл сентинел — потребителю пришлось бы сравнивать строки")
	}
}

// Ключ не должен попасть ни в текст ошибки, ни в её поля: ошибки уходят в
// журналы и в тикеты, а ключ арендатора открывает денежную дверь целиком.
func TestErrorTextCarriesNoKey(t *testing.T) {
	const body = `{"success":false,"error":{"code":"INSUFFICIENT_SCOPE","message":"scope missing"},"meta":{"request_id":"req_1"}}`
	srv := newStub(t, http.StatusForbidden, body, nil)
	c, _ := cpa.New("tk_super_secret_key", &cpa.ClientOptions{BaseURL: srv.URL})

	_, err := c.Do(context.Background(), http.MethodGet, "/cpa/offers", nil, nil)
	if err == nil {
		t.Fatal("403 пришёл без ошибки")
	}
	if strings.Contains(err.Error(), "tk_super_secret_key") {
		t.Errorf("текст ошибки несёт ключ: %s", err.Error())
	}
}
