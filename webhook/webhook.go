// Package webhook — проверка входящих вебхуков Recca.
//
// Сети в пакете нет: он проверяет подпись над байтами, которые ему дали.
// Контракт — spec/webhook.md; векторы testdata/webhook_vectors.json порождены
// живым кодом платформы.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// HeaderName — заголовок, в котором Recca присылает подпись вебхука.
const HeaderName = "X-Recca-Signature"

// Verify проверяет подпись входящего вебхука над СЫРЫМ телом — байт в байт,
// как оно пришло по сети, до всякого разбора JSON: пересборка тела меняет
// порядок ключей, пробелы и форму чисел, и подпись перестаёт сходиться.
//
// Порядок проверки:
//
//  1. разобрать заголовок "t=<unix_seconds>,v1=<hex_64>" — не разобрался, false;
//  2. свежесть: |now − t| ≤ tolerance, допуск двусторонний — вне допуска, false;
//  3. HMAC-SHA256(secret, "<t>.<body>") и константное сравнение (hmac.Equal).
//
// Просроченный запрос отбивается до сравнения подписи и не тратит его.
// Любой отсутствующий или повреждённый кусок — обычный отказ, а не паника:
// вебхук приходит из сети.
func Verify(secret, header string, body []byte, now time.Time, tolerance time.Duration) bool {
	ts, sig, ok := parseHeader(header)
	if !ok {
		return false
	}
	if !fresh(now, ts, tolerance) {
		return false
	}
	if len(sig) != sha256.Size*2 {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), got)
}

// parseHeader разбирает значение заголовка подписи: пары key=value через
// запятую, обязательные t и v1. Пара с нечисловой меткой или повторная —
// отсутствует; нет любой из двух — заголовок не разобран.
func parseHeader(header string) (ts int64, sig string, ok bool) {
	var haveTS, haveSig bool
	for _, part := range strings.Split(header, ",") {
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		switch key {
		case "t":
			if haveTS {
				continue
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				continue
			}
			ts, haveTS = n, true
		case "v1":
			if haveSig {
				continue
			}
			sig, haveSig = value, true
		}
	}
	if !haveTS || !haveSig {
		return 0, "", false
	}
	return ts, sig, true
}

// fresh — двусторонний допуск свежести: |now − t| ≤ tolerance, с той же
// семантикой на границе, что у postback.CheckFreshness: полное время, без
// усечения now до целых секунд.
//
// Сравнение идёт через time.Time, а не через разность секунд: модуль
// разности и умножение seconds*1e9 переполняются на метках дальше ~292 лет
// от now (год 9999, метка в миллисекундах) — завёрнутая длительность может
// стать отрицательной, и метка из далёкого будущего прошла бы гейт свежести.
func fresh(now time.Time, ts int64, tolerance time.Duration) bool {
	limit := time.Unix(ts, 0)
	return !now.Before(limit.Add(-tolerance)) && !now.After(limit.Add(tolerance))
}
