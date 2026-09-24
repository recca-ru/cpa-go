// Package cpatest — стенд для тестирования интеграции с CPA-API Recca.
//
// # Что этот стенд доказывает и чего он НЕ доказывает
//
// ⚠⚠ Стенд СТРУКТУРНЫЙ. Он отвечает на все восемнадцать путей конвертом
// контракта и фикстурами из testdata/fixtures — и этим доказывает, что ваш код
// собирает верные запросы и разбирает верные ответы. **Поведение платформы он не
// воспроизводит и не обещает воспроизводить ни строкой.** Что произойдёт с живой
// конверсией, как сойдётся тройка ставок, когда истечёт холд и как дверь ответит
// на вашу конкретную пару ключей — доказывает песочница api-sandbox.recca.ru и
// набор Conformance против неё, а не этот файл.
//
// Граница названа прямо, потому что стенд, который «почти как настоящий»,
// опаснее отсутствия стенда: зелёный прогон против него читается как готовность
// к бою.
//
// # Два правила, которые стенд всё-таки исполняет
//
// Идемпотентность приёма конверсии и заявки на выплату — единственная логика,
// кроме отдачи фикстур:
//
//   - повтор конверсии с той же тройкой «external_id + цель» возвращает ТУ ЖЕ
//     строку и 200 вместо 201;
//   - повтор заявки того же партнёра за тот же период с той же суммой — ту же
//     заявку; с другой суммой — 409 IDEMPOTENT_MISMATCH (сумма сверяется, а не
//     входит в ключ).
//
// Они здесь не ради полноты, а потому что их проверяет Conformance: набор,
// который нельзя прогнать против стенда, не был бы проверен вовсе до первого
// выхода на песочницу.
package cpatest

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"go.recca.ru/cpa"
)

// fixtures — тела ответов. Файлами, а не литералами в Go: правка контракта
// меняет один файл, и видно её в дифе как данные, а не как код.
//
//go:embed testdata/fixtures/*.json
var fixtures embed.FS

// Request — обращение, которое увидел стенд.
type Request struct {
	Method string
	// Path — путь ОТ версии двери: "/cpa/conversions".
	Path  string
	Query url.Values
	Body  []byte
}

// Server — стенд на httptest.
type Server struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []Request
	// pending — заготовленный отказ. held решает, снимается ли он после первого
	// же запроса (PrepareError) или держится до ClearError (HoldError).
	pending *cpa.ErrorCode
	held    bool
	// conversions и payouts держат идемпотентность; см. заголовок пакета.
	conversions map[string]json.RawMessage
	payouts     map[string]payoutRecord

	// tamper и repeatStatus — намеренные поломки стенда.
	//
	// ⚠⚠ Не экспортируются и наружу не выйдут. Читатель у них ровно один —
	// внутренние тесты, доказывающие, что Conformance КРАСНЕЕТ: на несходящейся
	// тройке ставок (tamper) и на повторе, ответившем 201 вместо 200
	// (repeatStatus). Без таких проверок набор был бы утверждением о себе самом:
	// зелёный он и на верном расчёте, и на сломанном, а отличить нельзя. Дать эти
	// ручки публично значило бы предложить интегратору способ сделать свой прогон
	// зелёным по неправильной причине.
	tamper func(json.RawMessage) json.RawMessage
	// repeatStatus — код ответа идемпотентного повтора; 0 означает штатные 200.
	repeatStatus int
	// canRequest подменяет can_request в балансе партнёра (nil — фикстура, true);
	// при false баланс показывает и нулевой доступный остаток.
	canRequest *bool
	// refusePayouts — дверь заявок отказывает INSUFFICIENT_BALANCE.
	//
	// Вдвоём они дают четыре мира: согласованный пустой баланс (оба), согласованный
	// полный (ни одного) и две лжи — баланс обещает заявку, а дверь отказывает, и
	// наоборот. Набор обязан краснеть на обеих лжах и зеленеть на обоих мирах.
	refusePayouts bool
	// environment подменяет среду в ответе /cpa/self ("" — фикстура, sandbox).
	// Читатель — внутренний тест, доказывающий, что Conformance не пишет ничего,
	// когда сервер называет не песочницу.
	environment string
}

type payoutRecord struct {
	amount int64
	body   json.RawMessage
}

// NewServer поднимает стенд и закрывает его по завершении теста.
func NewServer(t *testing.T) *Server {
	t.Helper()

	s := Start()
	t.Cleanup(s.srv.Close)
	return s
}

// Start поднимает стенд вне теста — там, где *testing.T нет: в исполняемых
// примерах (Example…) и в main-программах. Закрывает его вызывающий, через
// Close.
//
// ⚠ В тестах берите NewServer: он закроет стенд сам, а забытый Close здесь
// оставляет открытый порт до конца процесса.
func Start() *Server {
	s := &Server{
		conversions: map[string]json.RawMessage{},
		payouts:     map[string]payoutRecord{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// URL — базовый адрес стенда; передавайте его в cpa.ClientOptions.BaseURL.
func (s *Server) URL() string { return s.srv.URL }

// Client собирает клиента, настроенного на этот стенд.
//
// ⚠⚠ Повторы у этого клиента ВЫКЛЮЧЕНЫ (MaxRetries = 0), и это не мелочь
// настройки. Заготовленный PrepareError срабатывает ровно один раз — значит при
// включённых повторах отказ на ПОВТОРЯЕМОМ коде (500, 502, 429) съедала бы
// собственная политика клиента: первая попытка получает отказ, вторая — обычный
// ответ стенда, и тест ветки отказа зеленел бы по неправильной причине. Найдено
// прогоном, а не рассуждением.
//
// Работаете со стендом СВОИМ клиентом с повторами — заготавливайте отказ через
// HoldError, а не PrepareError.
func (s *Server) Client(t *testing.T) *cpa.Client {
	t.Helper()
	c, err := cpa.New("tk_cpatest", &cpa.ClientOptions{
		BaseURL:    s.srv.URL,
		MaxRetries: cpa.Ptr(0),
	})
	if err != nil {
		t.Fatalf("cpatest: клиент не собран: %v", err)
	}
	return c
}

// Close останавливает стенд. Звать вручную не обязательно: NewServer уже повесил
// закрытие на t.Cleanup.
func (s *Server) Close() { s.srv.Close() }

// PrepareError заготавливает отказ на СЛЕДУЮЩИЙ запрос — ровно один раз.
//
// ⚠ Один раз, а не «до отмены»: залипший отказ красит все последующие шаги
// цепочки, и разбираться придётся не с тем, что сломалось, а с тем, что стенд не
// отпустил.
//
// ⚠⚠ На ПОВТОРЯЕМОМ коде (500, 502, 503, 429) одноразовая заготовка надёжна ТОЛЬКО
// с клиентом из Server.Client: у клиента с повторами её съест его же политика
// повтора — первая попытка получит отказ, вторая обычный ответ стенда, и вызов
// закончится успехом. Для своего клиента с повторами — HoldError.
func (s *Server) PrepareError(code cpa.ErrorCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = &code
	s.held = false
}

// HoldError держит отказ на КАЖДОМ запросе — до явного ClearError.
//
// Режим нужен клиенту с повторами. Отличить повтор от нового вызова стенд не
// может: признака попытки на проводе нет, и заводить его ради стенда значило бы
// править ядро. Держать отказ «на все попытки одного вызова» он не может тоже — у
// клиента своё MaxRetries, и бюджета повторов стенд не знает. Поэтому отказ
// держится, пока его не снимут, и ваш код увидит его, сколько бы раз клиент ни
// повторил.
//
// ⚠ Не забывайте ClearError: пока отказ держится, отбивается ЛЮБОЙ запрос, в том
// числе следующий шаг вашей цепочки.
func (s *Server) HoldError(code cpa.ErrorCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = &code
	s.held = true
}

// ClearError снимает заготовленный отказ любого режима.
func (s *Server) ClearError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
	s.held = false
}

// Requests — всё, что стенд увидел, в порядке поступления.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

// Reset забывает записанные обращения и заготовленный отказ; состояние
// идемпотентности тоже.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
	s.pending = nil
	s.held = false
	s.conversions = map[string]json.RawMessage{}
	s.payouts = map[string]payoutRecord{}
}

// fixture читает тело из testdata/fixtures.
func fixture(name string) json.RawMessage {
	raw, err := fixtures.ReadFile("testdata/fixtures/" + name)
	if err != nil {
		// Фикстура встроена в двоичный файл: её отсутствие — это ошибка сборки
		// пакета, а не окружения, и молчать о ней нельзя.
		panic("cpatest: фикстура " + name + " не встроена: " + err.Error())
	}
	return json.RawMessage(raw)
}
