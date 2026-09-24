package cpatypes

import (
	"net/url"
	"strconv"
)

// PageParams — общая часть параметров страницы.
//
// ⚠⚠ Потолки limit и offset клиент НЕ проверяет, и это решение, а не пропуск.
// Числа уже разошлись между документом и кодом: openapi объявляет максимум 100
// (`components/parameters/Limit`), а дверь партнёров принимает 200
// (`ListSchema`, `api/federation/v1/cpa/partners/route.ts`). Третья копия здесь
// отказывала бы в том, что живая дверь принимает, — а отказ до сети имеет смысл
// только когда клиент прав наверняка. Негодную страницу дверь отбивает 400
// `INVALID_INPUT` и называет поле.
type PageParams struct {
	// Limit — сколько строк вернуть. 0 означает «умолчание двери», а не «ноль
	// строк»: просить ноль строк незачем, а вот не иметь мнения о размере
	// страницы — обычное дело.
	Limit int
	// Offset — сколько строк пропустить.
	Offset int
}

// addTo дописывает страницу в строку запроса. Ноль не пишется: пустой ключ
// отличим от заданного, и дверь применяет своё умолчание.
func (p PageParams) addTo(q url.Values) {
	if p.Limit != 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset != 0 {
		q.Set("offset", strconv.Itoa(p.Offset))
	}
}

// Validate — единственное, что клиент утверждает о странице сам: отрицательные
// величины бессмысленны при любом потолке двери.
func (p PageParams) Validate() error {
	if p.Limit < 0 {
		return invalid("limit", "отрицательный размер страницы")
	}
	if p.Offset < 0 {
		return invalid("offset", "отрицательное смещение")
	}
	return nil
}

// setIfNotEmpty — короткая форма для необязательного параметра строки запроса.
// Пустая строка не пишется: ключ с пустым значением дверь читает как фильтр по
// пустоте, а не как отсутствие фильтра.
func setIfNotEmpty(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}
