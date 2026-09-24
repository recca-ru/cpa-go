package cpatypes

import (
	"strconv"
	"strings"
	"time"
)

// dateLayout — форма даты без времени в контракте.
const dateLayout = "2006-01-02"

// Date — календарная дата без времени: период выплаты и границы отчёта.
//
// ⚠⚠ time.Time здесь не годится, и это не вкус: encoding/json разбирает time.Time
// только как RFC 3339, а дверь присылает «2026-09-01». Разбор упал бы на
// совершенно законном ответе — то есть ответ выглядел бы поломкой контракта.
//
// ⚠ У конверсий границы окна — НАПРОТИВ, полное время (`date-time`), и путать их
// нельзя: у выплаты период — это календарные сутки учёта, а у выборки конверсий —
// момент.
type Date struct {
	time.Time
}

// NewDate собирает дату из года, месяца и дня в UTC.
func NewDate(year int, month time.Month, day int) Date {
	return Date{time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// DateOf берёт календарную дату момента в его же зоне.
func DateOf(t time.Time) Date {
	year, month, day := t.Date()
	return Date{time.Date(year, month, day, 0, 0, 0, 0, t.Location())}
}

// ParseDate разбирает дату контрактной формы.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, invalid("date", "ожидается дата вида 2006-01-02, пришло "+strconv.Quote(s))
	}
	return Date{t}, nil
}

// String — дата контрактной формой.
func (d Date) String() string { return d.Format(dateLayout) }

// MarshalJSON пишет дату строкой без времени.
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(d.String())), nil
}

// UnmarshalJSON принимает и дату, и полное время: дверь объявляет `date`, но
// ответ с полным временем разбирать всё равно лучше, чем ронять разбор всей
// карточки из-за формы одного поля.
func (d *Date) UnmarshalJSON(raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if text == "null" {
		return nil
	}
	unquoted, err := strconv.Unquote(text)
	if err != nil {
		return invalid("date", "значение не является строкой JSON: "+text)
	}
	if unquoted == "" {
		return nil
	}
	if parsed, err := time.Parse(dateLayout, unquoted); err == nil {
		d.Time = parsed
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, unquoted)
	if err != nil {
		return invalid("date", "ожидается дата вида 2006-01-02, пришло "+strconv.Quote(unquoted))
	}
	d.Time = parsed
	return nil
}

// IsZero отвечает, задана ли дата вообще.
func (d Date) IsZero() bool { return d.Time.IsZero() }
