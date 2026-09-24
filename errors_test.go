// Контракт ошибок: реестр кодов, сентинелы и то, что клиент делает с кодом,
// которого он ещё не знает.
//
// ⚠⚠ Реестр сверяется с ФАЙЛОМ контракта (testdata/error_codes.json), а не с
// литералами в этом тесте. Копия таблицы, набранная здесь руками, отстала бы от
// Recca молча и при этом выглядела бы проверкой — тот самый класс, за который
// проект ругает фикстуры, дублирующие данные предмета.
package cpa_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"go.recca.ru/cpa"
)

const errorCodesPath = "testdata/error_codes.json"

type contractCodes struct {
	ContractVersion string `json:"contract_version"`
	Codes           []struct {
		Code string `json:"code"`
		HTTP int    `json:"http"`
	} `json:"codes"`
}

func loadContractCodes(t *testing.T) contractCodes {
	t.Helper()
	raw, err := os.ReadFile(errorCodesPath)
	if err != nil {
		t.Fatalf("реестр кодов не прочитан (%s): %v", errorCodesPath, err)
	}
	var c contractCodes
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("реестр кодов не разобран: %v", err)
	}
	if len(c.Codes) == 0 {
		t.Fatal("в реестре ноль кодов — сверять нечем, а тест выглядел бы зелёным")
	}
	return c
}

// Сверка ДВУСТОРОННЯЯ. Код контракта, которого нет у клиента, — дыра в
// ветвлении; код клиента, которого нет в контракте, — выдумка, по которой
// потребитель напишет мёртвую ветку. Единственное объявленное исключение —
// MALFORMED_RESPONSE: его ставит сам клиент, дверь такого не присылает.
func TestErrorRegistryMatchesContract(t *testing.T) {
	contract := loadContractCodes(t)

	inContract := make(map[cpa.ErrorCode]int, len(contract.Codes))
	for _, c := range contract.Codes {
		code := cpa.ErrorCode(c.Code)
		inContract[code] = c.HTTP

		status, known := cpa.HTTPStatusFor(code)
		if !known {
			t.Errorf("код контракта %q клиенту неизвестен", code)
			continue
		}
		if status != c.HTTP {
			t.Errorf("код %q: клиент ждёт HTTP %d, контракт называет %d", code, status, c.HTTP)
		}
	}

	clientOnly := map[cpa.ErrorCode]bool{cpa.CodeMalformedResponse: true}
	for _, code := range cpa.Codes() {
		if _, ok := inContract[code]; ok {
			continue
		}
		if clientOnly[code] {
			continue
		}
		t.Errorf("клиент знает код %q, которого в контракте нет", code)
	}
}

// У каждого кода — свой сентинел, и сентинелы не повторяются: два кода с одним
// сентинелом означали бы, что errors.Is не различает разные отказы, а
// потребитель об этом не узнает.
func TestEveryCodeHasOwnSentinel(t *testing.T) {
	seen := map[error]cpa.ErrorCode{}
	for _, code := range cpa.Codes() {
		sentinel := cpa.SentinelFor(code)
		if sentinel == nil {
			t.Errorf("у кода %q нет сентинела", code)
			continue
		}
		if prev, dup := seen[sentinel]; dup {
			t.Errorf("коды %q и %q делят один сентинел %v", prev, code, sentinel)
			continue
		}
		seen[sentinel] = code
	}
	if len(seen) < len(cpa.Codes()) {
		t.Errorf("сентинелов %d на %d кодов", len(seen), len(cpa.Codes()))
	}
}

// errors.Is добирается до сентинела через Unwrap — это и есть обещанный способ
// ветвиться по коду, не сравнивая строки.
func TestAPIErrorUnwrapsToSentinel(t *testing.T) {
	for _, code := range cpa.Codes() {
		apiErr := &cpa.APIError{Code: code, HTTPStatus: 400, Message: "-"}
		sentinel := cpa.SentinelFor(code)
		if !errors.Is(apiErr, sentinel) {
			t.Errorf("errors.Is(%q, его сентинел) = false", code)
		}
		if errors.Is(apiErr, cpa.ErrValidation) {
			t.Errorf("ошибка двери %q выдала себя за ошибку проверки до сети", code)
		}
	}
}

// Повторяемость — свойство отказа, а не догадка вызывающего. Коды исчерпания
// бюджета и внутреннего сбоя переживают повтор; отказ по праву, по входу или по
// занятому ключу идемпотентности не изменится, сколько ни повторяй.
//
// ⚠ Retryable НЕ означает «клиент повторит сам»: повтор делает транспорт и
// только для операций, объявленных идемпотентными. Это разные решения, и
// сводить их нельзя — повтор неидемпотентного POST оплатил бы конверсию дважды.
func TestRetryableByCodeAndStatus(t *testing.T) {
	cases := []struct {
		name   string
		code   cpa.ErrorCode
		status int
		want   bool
	}{
		{"исчерпан бюджет", cpa.CodeRateLimitExceeded, http.StatusTooManyRequests, true},
		{"внутренний сбой", cpa.CodeInternalError, http.StatusInternalServerError, true},
		{"возврат не завершился", cpa.CodeReversalFailed, http.StatusInternalServerError, true},
		{"нет права", cpa.CodeInsufficientScope, http.StatusForbidden, false},
		{"продукт на паузе", cpa.CodeProductPaused, http.StatusForbidden, false},
		{"вход не прошёл проверку", cpa.CodeInvalidInput, http.StatusBadRequest, false},
		{"ключ идемпотентности занят", cpa.CodeIdempotentMismatch, http.StatusConflict, false},
		{"персональные данные", cpa.CodePIINotAccepted, http.StatusBadRequest, false},
		{"зарезервированный префикс", cpa.CodeReservedExternalID, http.StatusBadRequest, false},
		// Код новый и клиенту неизвестен, но статус 5xx говорит о сбое сам.
		{"неизвестный код при 503", cpa.ErrorCode("SOME_NEW_CODE"), http.StatusServiceUnavailable, true},
		{"неизвестный код при 400", cpa.ErrorCode("SOME_NEW_CODE"), http.StatusBadRequest, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cpa.Retryable(tc.code, tc.status)
			if got != tc.want {
				t.Errorf("Retryable(%q, %d) = %v, ждали %v", tc.code, tc.status, got, tc.want)
			}
		})
	}
}

// Новый код на новом условии — совместимая правка контракта (раздел 15). Клиент
// обязан донести её до потребителя, а не подменить своим классом: иначе
// интегратор увидел бы «внутреннюю ошибку» там, где сервер назвал причину.
func TestUnknownCodeSurvivesTyped(t *testing.T) {
	apiErr := &cpa.APIError{Code: "TOTALLY_NEW_CODE", HTTPStatus: http.StatusConflict, Message: "что-то новое"}

	if got, want := apiErr.Code, cpa.ErrorCode("TOTALLY_NEW_CODE"); got != want {
		t.Errorf("Code = %q, ждали %q", got, want)
	}
	if cpa.SentinelFor(apiErr.Code) != nil {
		t.Error("у неизвестного кода появился сентинел — значит реестр его знает, а тест лжёт")
	}
	if errors.Unwrap(apiErr) != nil {
		t.Error("Unwrap неизвестного кода обязан быть nil, иначе errors.Is совпадёт с чужим сентинелом")
	}
	var as *cpa.APIError
	if !errors.As(apiErr, &as) {
		t.Error("errors.As не распознал *APIError")
	}
}

// Сообщение ошибки — строка для человека в тикете: код, статус и
// идентификатор запроса, по которому нас просят искать (раздел 4).
func TestAPIErrorMessageNamesCodeStatusAndRequestID(t *testing.T) {
	apiErr := &cpa.APIError{
		Code:       cpa.CodeWindowExpired,
		HTTPStatus: http.StatusConflict,
		Message:    "attribution window has expired",
		RequestID:  "req_01K5XYZ",
	}
	text := apiErr.Error()

	for _, want := range []string{"WINDOW_EXPIRED", "409", "req_01K5XYZ", "attribution window has expired"} {
		if !strings.Contains(text, want) {
			t.Errorf("в тексте ошибки нет %q: %s", want, text)
		}
	}

	// Без идентификатора текст обязан остаться читаемым, а не нести пустой хвост.
	bare := (&cpa.APIError{Code: cpa.CodeInvalidInput, HTTPStatus: 400, Message: "bad field"}).Error()
	if strings.Contains(bare, "request") {
		t.Errorf("текст без request_id упоминает его: %s", bare)
	}
}

// Ошибка проверки ДО сети отличима от отказа двери: у неё свой сентинел и имя
// поля. Иначе потребитель не понял бы, кто отказал — мы или Recca.
func TestValidationErrorIsDistinct(t *testing.T) {
	err := &cpa.ValidationError{Field: "click_id", Reason: "обязательное поле"}
	if !errors.Is(err, cpa.ErrValidation) {
		t.Error("errors.Is(ошибка проверки, ErrValidation) = false")
	}
	var apiErr *cpa.APIError
	if errors.As(err, &apiErr) {
		t.Error("ошибка проверки выдала себя за ответ двери")
	}
	if !strings.Contains(err.Error(), "click_id") {
		t.Errorf("ошибка не называет поле: %s", err.Error())
	}
}
