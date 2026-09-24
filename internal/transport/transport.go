// Package transport — исполнитель HTTP-запросов к CPA-API: повторы, паузы,
// разбор конверта и отладочный вывод.
//
// Пакет внутренний и выделен намеренно: проба WV-0698 на шести моделях
// показала, что дефекты живут не в методах, а здесь — в решении об ошибке, в
// чтении тела и в политике повтора. Отдельный пакет означает, что это решение
// принимается ОДИН раз, а не повторяется в каждом из восемнадцати методов.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.recca.ru/cpa/cpatypes"
)

const (
	// APIPathPrefix — версионированный префикс двери. Живёт здесь, а не в
	// каждом методе: волна смены мажорной версии правит одно место.
	APIPathPrefix = "/federation/v1"

	// DefaultMaxResponseBytes — потолок тела ответа. Без него сервер,
	// ответивший гигабайтом, съел бы память процесса интегратора.
	DefaultMaxResponseBytes int64 = 8 << 20

	// backoffBase — первая пауза перед повтором; дальше удваивается.
	backoffBase = 500 * time.Millisecond
	// jitterFraction — разброс паузы. Нужен, чтобы сотня клиентов не пришла
	// повторяться в одну миллисекунду после общего сбоя.
	jitterFraction = 0.25
	// retryAfterCap — сколько мы готовы ждать по указанию сервера. Пауза
	// длиннее означает отказ: высиживать минуту внутри чужого запроса значит
	// держать его горутину и его таймаут.
	retryAfterCap = 30 * time.Second

	// defaultTimeout — предел ожидания ответа, когда свой клиент не задан.
	defaultTimeout = 30 * time.Second
)

// Options — настройки исполнителя. Собираются клиентом один раз.
type Options struct {
	BaseURL    string
	Key        string
	UserAgent  string
	HTTPClient *http.Client
	MaxRetries int
	Logger     *slog.Logger
	LogHeaders bool
	// MaxResponseBytes — потолок тела; 0 означает DefaultMaxResponseBytes.
	MaxResponseBytes int64

	// Sleep и Jitter подменяются в тестах: без них проверка политики повторов
	// стоила бы секунд ожидания на каждый случай. Пакет внутренний, наружу эти
	// поля не видны.
	Sleep  func(ctx context.Context, d time.Duration) error
	Jitter func() float64
}

// Request — одно обращение к двери.
type Request struct {
	Method string
	// Path — путь ОТ версии двери: "/cpa/conversions", а не
	// "/federation/v1/cpa/conversions".
	Path  string
	Query url.Values
	Body  any
	// Out — куда разобрать data успешного ответа. nil означает, что данные
	// вызывающему не нужны.
	Out any
	// Retryable объявляет операцию идемпотентной.
	//
	// ⚠⚠ Решение принимает ВЫЗЫВАЮЩИЙ, и транспорт его не угадывает: ответ мог
	// потеряться уже после того, как конверсия записана, и повтор
	// неидемпотентного POST оплатил бы её вторично.
	Retryable bool
}

// Executor исполняет запросы. Неизменяем после New.
type Executor struct {
	baseURL    string
	key        string
	userAgent  string
	http       *http.Client
	maxRetries int
	logger     *slog.Logger
	logHeaders bool
	maxBytes   int64
	sleep      func(ctx context.Context, d time.Duration) error
	jitter     func() float64
}

// New собирает исполнителя.
func New(o Options) (*Executor, error) {
	if strings.TrimSpace(o.BaseURL) == "" {
		return nil, errors.New("cpa: базовый адрес не задан")
	}
	if strings.TrimSpace(o.Key) == "" {
		return nil, errors.New("cpa: ключ арендатора не задан")
	}
	if o.MaxRetries < 0 {
		return nil, fmt.Errorf("cpa: число повторов %d отрицательно", o.MaxRetries)
	}

	e := &Executor{
		baseURL:    strings.TrimRight(o.BaseURL, "/"),
		key:        o.Key,
		userAgent:  o.UserAgent,
		http:       o.HTTPClient,
		maxRetries: o.MaxRetries,
		logger:     o.Logger,
		logHeaders: o.LogHeaders,
		maxBytes:   o.MaxResponseBytes,
		sleep:      o.Sleep,
		jitter:     o.Jitter,
	}
	if e.http == nil {
		e.http = &http.Client{Timeout: defaultTimeout}
	}
	if e.maxBytes <= 0 {
		e.maxBytes = DefaultMaxResponseBytes
	}
	if e.sleep == nil {
		e.sleep = sleepContext
	}
	if e.jitter == nil {
		e.jitter = func() float64 { return rand.Float64()*2 - 1 }
	}
	return e, nil
}

// Do исполняет запрос, повторяя его по политике, объявленной в Request.
func (e *Executor) Do(ctx context.Context, r Request) (*cpatypes.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cpa: запрос не отправлен: %w", err)
	}
	if !strings.HasPrefix(r.Path, "/") {
		return nil, fmt.Errorf("cpa: путь %q обязан начинаться со слеша", r.Path)
	}

	body, err := encodeBody(r.Body)
	if err != nil {
		return nil, err
	}

	maxAttempts := 1
	if r.Retryable {
		maxAttempts += e.maxRetries
	}

	for attempt := 1; ; attempt++ {
		res := e.attempt(ctx, r, body, attempt)

		wait, retry := e.nextWait(res, attempt, maxAttempts)
		if !retry {
			return res.resp, res.err
		}
		if err := e.sleep(ctx, wait); err != nil {
			return nil, fmt.Errorf("cpa: ожидание перед повтором прервано: %w", err)
		}
	}
}

// attemptResult — исход одной попытки.
type attemptResult struct {
	resp *cpatypes.Response
	err  error
	// networkFailure означает, что ответа не было вовсе: до сервера не дошли.
	// Это единственный класс, где повтор оправдан без взгляда на статус.
	networkFailure bool
}

func (e *Executor) attempt(ctx context.Context, r Request, body []byte, attempt int) attemptResult {
	address := e.baseURL + APIPathPrefix + r.Path
	if len(r.Query) > 0 {
		address += "?" + r.Query.Encode()
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, address, reader)
	if err != nil {
		return attemptResult{err: fmt.Errorf("cpa: %s %s: %w", r.Method, r.Path, err)}
	}
	// ⚠ Ключ арендатора уходит ТОЛЬКО заголовком: строка запроса целиком
	// попадает в журналы nginx и в Referer каждого подресурса.
	req.Header.Set("X-Recca-Tenant-Key", e.key)
	req.Header.Set("Accept", "application/json")
	if e.userAgent != "" {
		req.Header.Set("User-Agent", e.userAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	started := time.Now()
	httpResp, err := e.http.Do(req)
	if err != nil {
		e.log(r, attempt, 0, time.Since(started), "", req.Header, nil, err)
		return attemptResult{
			err:            fmt.Errorf("cpa: %s %s: %w", r.Method, r.Path, err),
			networkFailure: true,
		}
	}
	defer func() { _ = httpResp.Body.Close() }()

	raw, err := readCapped(httpResp.Body, e.maxBytes)
	if err != nil {
		e.log(r, attempt, httpResp.StatusCode, time.Since(started), "", req.Header, httpResp.Header, err)
		return attemptResult{err: err}
	}

	resp, apiErr := e.interpret(httpResp, raw, r.Out)
	e.log(r, attempt, httpResp.StatusCode, time.Since(started), resp.RequestID, req.Header, httpResp.Header, apiErr)
	if apiErr != nil {
		return attemptResult{resp: resp, err: apiErr}
	}
	return attemptResult{resp: resp}
}

// envelope — конверт ответа (раздел 4 контракта).
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *errorPayload   `json:"error"`
	Meta    *cpatypes.Meta  `json:"meta"`
}

type errorPayload struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

// interpret разбирает ответ: что это — успех, отказ двери или ответ не по
// контракту.
//
// ⚠⚠ Решение принимается по HTTP-статусу И по флагу конверта, а не по одному
// из них: это ровно тот дефект, который проба WV-0698 нашла у всех моделей.
// Классов отказа два, и различаются они тем, ЧТО сообщает отказ, — статус для
// не-2xx, собственный код для 2xx с success:false.
func (e *Executor) interpret(httpResp *http.Response, raw []byte, out any) (*cpatypes.Response, error) {
	resp := &cpatypes.Response{
		StatusCode: httpResp.StatusCode,
		Header:     httpResp.Header,
	}

	var env envelope
	parsed := json.Unmarshal(bytes.TrimSpace(raw), &env) == nil
	if parsed && env.Meta != nil {
		resp.Meta = *env.Meta
	}

	// ⚠ Идентификатор берётся из ЗАГОЛОВКА: два отказа формируются до того,
	// как запрос дойдёт до метода, и тела с meta у них нет вовсе.
	resp.RequestID = httpResp.Header.Get("X-Request-Id")
	if resp.RequestID == "" {
		resp.RequestID = resp.Meta.RequestID
	}

	var payload *errorPayload
	if parsed {
		payload = env.Error
	}

	switch {
	case !isSuccessStatus(httpResp.StatusCode):
		return resp, e.apiError(resp, payload, "")

	case !parsed:
		return resp, e.apiError(resp, nil, "дверь ответила 2xx телом не по контракту")

	case !env.Success:
		// Статус 2xx об отказе не сообщает ничего, поэтому и код нужен
		// собственный: HTTP_200 в журнале потребителя читался бы как успех.
		return resp, e.apiError(resp, payload, "конверт помечен неуспешным")
	}

	if out != nil {
		data := bytes.TrimSpace(env.Data)
		if len(data) == 0 || bytes.Equal(data, []byte("null")) {
			// Наследие LOOP-01: пустая data при успехе — не «ничего не
			// нашли», а нарушение контракта. Разобрав её молча, мы отдали бы
			// нулевую структуру, то есть суммы, которых дверь не присылала.
			return resp, e.apiError(resp, nil, "успешный ответ без data")
		}
		if err := json.Unmarshal(data, out); err != nil {
			return resp, fmt.Errorf("cpa: разбор data (request_id %s): %w", resp.RequestID, err)
		}
	}
	return resp, nil
}

// apiError собирает типизированный отказ. Код берётся из конверта; когда двери
// нечего было сказать — выводится из статуса.
func (e *Executor) apiError(resp *cpatypes.Response, payload *errorPayload, fallbackMessage string) *cpatypes.APIError {
	code := cpatypes.CodeMalformedResponse
	message := fallbackMessage
	var details json.RawMessage

	switch {
	case payload != nil && payload.Code != "":
		code = cpatypes.ErrorCode(payload.Code)
		details = payload.Details
		if payload.Message != "" {
			message = payload.Message
		}
	case resp.StatusCode >= http.StatusInternalServerError:
		// Отказ шлюза (502, 503, 504) конверта не несёт, но для потребителя
		// это ровно «сбой на нашей стороне».
		code = cpatypes.CodeInternalError
	}

	if message == "" {
		if text := http.StatusText(resp.StatusCode); text != "" {
			message = text
		} else {
			message = "ответ без пояснения"
		}
	}

	return &cpatypes.APIError{
		Code:       code,
		Message:    message,
		HTTPStatus: resp.StatusCode,
		RequestID:  resp.RequestID,
		Details:    details,
		Retryable:  cpatypes.Retryable(code, resp.StatusCode),
	}
}

// nextWait решает, повторять ли, и сколько ждать.
func (e *Executor) nextWait(res attemptResult, attempt, maxAttempts int) (time.Duration, bool) {
	if res.err == nil || attempt >= maxAttempts {
		return 0, false
	}
	if res.networkFailure {
		return e.backoff(attempt), true
	}

	var apiErr *cpatypes.APIError
	if !errors.As(res.err, &apiErr) {
		// Тело сверх потолка, неразобранная data — отказы детерминированные:
		// повтор дал бы тот же ответ и потратил бы чужой бюджет.
		return 0, false
	}

	switch {
	case apiErr.HTTPStatus == http.StatusTooManyRequests:
		// ⚠ Паузу здесь называет сервер: бюджет считает он, а не мы.
		wait, ok := retryAfter(res.resp)
		if !ok {
			return e.backoff(attempt), true
		}
		if wait > retryAfterCap {
			return 0, false
		}
		return wait, true

	case apiErr.HTTPStatus >= http.StatusInternalServerError:
		return e.backoff(attempt), true
	}
	return 0, false
}

// backoff — пауза перед попыткой номер attempt+1: база, удвоенная по числу
// уже сделанных попыток, с разбросом ±25 %.
func (e *Executor) backoff(attempt int) time.Duration {
	base := backoffBase << (attempt - 1)
	spread := float64(base) * jitterFraction * e.jitter()
	wait := time.Duration(float64(base) + spread)
	if wait < 0 {
		return 0
	}
	return wait
}

// retryAfter читает паузу, названную сервером. Секунды — единственная форма,
// которую отдаёт дверь; дату в этом заголовке она не присылает.
func retryAfter(resp *cpatypes.Response) (time.Duration, bool) {
	if resp == nil || resp.Header == nil {
		return 0, false
	}
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func encodeBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("cpa: сборка тела запроса: %w", err)
	}
	return raw, nil
}

// readCapped читает тело ОДИН раз и с потолком.
//
// ⚠ Читаем целиком до разбора намеренно: декодер по потоку оставил бы тело
// недочитанным, соединение — непригодным для повторного использования, а
// сообщение об ошибке — без самого ответа.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("cpa: чтение ответа: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("cpa: тело ответа длиннее потолка в %d байт", limit)
	}
	return data, nil
}

func isSuccessStatus(status int) bool { return status >= 200 && status <= 299 }

// sleepContext — ожидание, прерываемое отменой. ⚠ time.Sleep здесь не годится:
// он продержал бы горутину полсекунды после того, как ответ уже никому не
// нужен.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// loggedRequestHeaders и loggedResponseHeaders — заголовки, которые попадают в
// отладочный вывод. Список, а не вычитание: новый заголовок с чувствительным
// значением не должен попадать в журнал сам собой.
var (
	loggedRequestHeaders  = []string{"User-Agent", "Content-Type", "X-Recca-Tenant-Key"}
	loggedResponseHeaders = []string{"X-Request-Id", "Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "Content-Type"}
)

// log пишет отладочную строку о попытке.
//
// ⚠⚠ Тела нет НИКОГДА — ни запроса, ни ответа: там едут суммы, номера заказов
// и метки источников трафика, а журнал потребителя живёт дольше и читается
// шире, чем он думает. Ключ арендатора маскируется даже при LogHeaders.
func (e *Executor) log(r Request, attempt, status int, took time.Duration, requestID string, reqHeaders, respHeaders http.Header, err error) {
	if e.logger == nil {
		return
	}

	attrs := []any{
		slog.String("method", r.Method),
		slog.String("path", APIPathPrefix+r.Path),
		slog.Int("attempt", attempt),
		slog.Int64("took_ms", took.Milliseconds()),
	}
	if status != 0 {
		attrs = append(attrs, slog.Int("status", status))
	}
	if requestID != "" {
		attrs = append(attrs, slog.String("request_id", requestID))
	}
	if err != nil {
		var apiErr *cpatypes.APIError
		if errors.As(err, &apiErr) {
			attrs = append(attrs, slog.String("error_code", string(apiErr.Code)))
		} else {
			attrs = append(attrs, slog.String("error", err.Error()))
		}
	}
	if e.logHeaders {
		attrs = append(attrs,
			slog.Any("request_headers", pickHeaders(reqHeaders, loggedRequestHeaders)),
			slog.Any("response_headers", pickHeaders(respHeaders, loggedResponseHeaders)),
		)
	}

	e.logger.Debug("cpa: запрос", attrs...)
}

func pickHeaders(h http.Header, names []string) map[string]string {
	out := make(map[string]string, len(names))
	if h == nil {
		return out
	}
	for _, name := range names {
		value := h.Get(name)
		if value == "" {
			continue
		}
		if strings.EqualFold(name, "X-Recca-Tenant-Key") {
			value = maskKey(value)
		}
		out[name] = value
	}
	return out
}

// maskKey оставляет от ключа только начало: различить два ключа в журнале
// нужно, прочитать их оттуда — нет.
func maskKey(key string) string {
	const shown = 4
	if len(key) <= shown {
		return "…"
	}
	return key[:shown] + "…(" + strconv.Itoa(len(key)) + ")"
}
