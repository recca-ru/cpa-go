// Package postback — подпись и проверка S2S-постбэка CPA.
//
// Сети в пакете нет: он считает HMAC над строками, которые ему дали.
// Контракт — spec/postback.md; векторы testdata/signature_vectors.json
// порождены живым кодом платформы, и расходиться с ними нельзя.
package postback

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Fields — поля постбэка, входящие в подпись.
//
// Три поля — указатели, потому что у них «не передали» и «передали пустым»
// означают разное, а канон решает ПРИСУТСТВИЕМ ключа, не значением:
//
//	goal      nil → 5 сегментов; "" → 6 сегментов
//	timestamp nil → метки нет; "" → метка есть (пустая), форма всё равно 7
//	sum_rub   nil → сегмент ""; 0 → сегмент "0"
//
// SumRUB — строка намеренно: в канон уходит лексическая форма числа, как её
// дал отправитель (12.5 обязана остаться 12.5, а не стать 12.50). Денежный
// тип с собственным форматированием сломал бы подпись молча.
type Fields struct {
	ClickID    string
	ExternalID string
	Status     string
	SumRUB     *string
	SubID      string
	Goal       *string
	Timestamp  *string
}

// Canonicalize собирает каноническую строку подписи. Число сегментов
// однозначно называет форму:
//
//	5 — click_id:external_id:status:sum_rub:sub_id                (легаси)
//	6 — …:sub_id:goal                                             (легаси + цель)
//	7 — …:sub_id:goal:timestamp                                   (полная)
//
// Метка времени форсирует семь сегментов всегда: без неё шесть означали бы
// и цель, и метку, а отличить их по содержимому нельзя — цель бывает числовой.
func Canonicalize(f Fields) string {
	segments := make([]string, 0, 7)
	segments = append(segments, f.ClickID, f.ExternalID, f.Status, value(f.SumRUB), f.SubID)
	if f.Goal != nil {
		segments = append(segments, *f.Goal)
	}
	if f.Timestamp != nil {
		if f.Goal == nil {
			segments = append(segments, "") // шестой сегмент пуст: цели не было
		}
		segments = append(segments, *f.Timestamp)
	}
	return strings.Join(segments, ":")
}

// value — сегмент указательного поля: nil означает «не передали», то есть
// пустую строку с сохранением разделителя.
func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Sign считает подпись канона: HMAC-SHA256 секретом оффера, hex в нижнем
// регистре, 64 символа.
func Sign(f Fields, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(Canonicalize(f)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify проверяет подпись постбэка. Подпись обязана быть ровно 64
// hex-символами — иначе false ещё до сравнения. Само сравнение константно по
// времени (hmac.Equal): оператор == сравнивает с ранним выходом и раскрывает
// подпись по времени ответа.
func Verify(f Fields, secret, signatureHex string) bool {
	if len(signatureHex) != sha256.Size*2 {
		return false
	}
	got, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(Canonicalize(f)))
	return hmac.Equal(mac.Sum(nil), got)
}

// SecureToken — токен GET-двери: "cpag_" + HMAC-SHA256(secret, offer_id) в hex.
// В строке запроса ездит токен, а не сам secret: query целиком попадает в
// журналы nginx и в Referer. Ротация секрета отзывает токен автоматически.
func SecureToken(secret, offerID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(offerID))
	return "cpag_" + hex.EncodeToString(mac.Sum(nil))
}

// CheckFreshness — истина, когда метка ts в допуске: |now − ts| ≤ tolerance.
// Допуск ДВУСТОРОННИЙ: метка из будущего — такая же подделка свежести, как из
// прошлого. Метка в секундах Unix; платформа использует допуск 300 секунд.
//
// Сравнение идёт через time.Time, а не через разность длительностей:
// Time.Sub насыщается за ~292 года, и для неотличимой на глаз ошибки
// интеграции (метка в миллисекундах) насыщенная разность после смены знака
// остаётся отрицательной — метка из далёкого будущего проходила бы как
// свежая. Модуль и умножение секунд на 1e9 переполняются на тех же входах.
func CheckFreshness(ts int64, now time.Time, tolerance time.Duration) bool {
	limit := time.Unix(ts, 0)
	return !now.Before(limit.Add(-tolerance)) && !now.After(limit.Add(tolerance))
}
