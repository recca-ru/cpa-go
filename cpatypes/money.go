package cpatypes

import (
	"encoding/json"
	"strconv"
	"strings"
)

// RUB — сумма в рублях, целым числом.
//
// ⚠⚠ Тип целый намеренно: float64 на деньгах даёт ошибку округления, которую
// не видно до сверки с кассой. Дверь при этом принимает и дробную сумму
// (`sum_rub: 12.5`), и для неё есть отдельный путь — SumRUBExact, где число
// едет ЛЕКСИЧЕСКОЙ формой, как его дал отправитель. Иначе 12.5 стало бы 12.50
// и подпись постбэка перестала бы сходиться молча.
type RUB int64

// String — сумма без обозначения валюты: подставлять «₽» за потребителя значит
// решать за его интерфейс.
func (r RUB) String() string { return strconv.FormatInt(int64(r), 10) }

// ClickID — идентификатор перехода, который вернул POST /cpa/clicks.
type ClickID string

// PartnerExternalID — идентификатор партнёра, назначенный ИНТЕГРАТОРОМ.
//
// Через границу идут только непрозрачные идентификаторы (раздел 3): имя, почта
// и телефон партнёра остаются у интегратора и в Recca не попадают ни в одну
// таблицу.
type PartnerExternalID string

// Validate проверяет форму: алфавит [A-Za-z0-9_.:-] и длину до 64 символов.
//
// ⚠ Проверка ограничивает ФОРМУ, а не смысл: строка вроде 79991234567 алфавит
// проходит. Обязательство не присылать персональные данные — договорное, и
// делать вид, что его исполняет клиент, было бы обещанием, которого он не
// держит.
func (id PartnerExternalID) Validate() error {
	switch {
	case id == "":
		return invalid("partner_external_id", "обязательное поле")
	case len(id) > MaxPartnerExternalIDLen:
		return invalid("partner_external_id", "длиннее "+strconv.Itoa(MaxPartnerExternalIDLen)+" символов")
	}
	for _, r := range string(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '.', r == ':', r == '-':
		default:
			return invalid("partner_external_id", "знак "+strconv.QuoteRune(r)+" вне алфавита [A-Za-z0-9_.:-]")
		}
	}
	return nil
}

// validNumber отвечает, является ли лексическая форма числом JSON. Проверяем
// сами: json.Number — это строка, и негодное значение уехало бы в тело как
// есть, сделав его неразбираемым уже на стороне двери.
func validNumber(n json.Number) bool {
	s := strings.TrimSpace(n.String())
	if s == "" || s != n.String() {
		return false
	}
	if _, err := n.Float64(); err != nil {
		return false
	}
	return true
}
