// Пакет `webhook` — проверка ВХОДЯЩИХ вебхуков Recca.
//
// Тесты написаны до реализации и падают, пока её нет.
//
// ⚠⚠ Подписывается СЫРОЕ тело, байт в байт, как оно пришло по сети. Не
// результат разбора JSON и не его повторная сериализация: порядок ключей,
// пробелы и форма чисел при пересборке меняются, и подпись перестаёт
// сходиться. Вектор `raw_body_signed_verbatim` заведён ровно на это — его тело
// намеренно не в канонической форме.
package webhook_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.recca.ru/cpa/webhook"
)

const vectorsPath = "../testdata/webhook_vectors.json"

type hookCase struct {
	Name           string `json:"name"`
	What           string `json:"what"`
	Timestamp      int64  `json:"timestamp"`
	RawBody        string `json:"raw_body"`
	Header         string `json:"header"`
	TamperedHeader string `json:"tampered_header"`
}

type hookVectors struct {
	ContractVersion string     `json:"contract_version"`
	Secret          string     `json:"secret"`
	HeaderName      string     `json:"header_name"`
	ToleranceSec    int        `json:"tolerance_sec"`
	Cases           []hookCase `json:"cases"`
}

func load(t *testing.T) hookVectors {
	t.Helper()
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("векторы не прочитаны (%s): %v", vectorsPath, err)
	}
	var v hookVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("векторы не разобраны: %v", err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("в векторах нет ни одного случая — проверять нечего")
	}
	return v
}

func (v hookVectors) tolerance() time.Duration {
	return time.Duration(v.ToleranceSec) * time.Second
}

func TestVerifyAcceptsValidSignature(t *testing.T) {
	v := load(t)
	for _, c := range v.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			now := time.Unix(c.Timestamp, 0)
			if !webhook.Verify(v.Secret, c.Header, []byte(c.RawBody), now, v.tolerance()) {
				t.Fatalf("верная подпись отвергнута\n  почему случай важен: %s", c.What)
			}
		})
	}
}

// Каждый положительный вектор приносит СВОЙ отрицательный: так число случаев
// не зависит от того, сколько у нас видов подделки.
func TestVerifyRejectsTamperedSignature(t *testing.T) {
	v := load(t)
	for _, c := range v.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			if c.TamperedHeader == "" || c.TamperedHeader == c.Header {
				t.Fatalf("у случая нет испорченного заголовка — проверка была бы пустой")
			}
			now := time.Unix(c.Timestamp, 0)
			if webhook.Verify(v.Secret, c.TamperedHeader, []byte(c.RawBody), now, v.tolerance()) {
				t.Fatal("подпись с изменённым последним знаком обязана быть отвергнута")
			}
		})
	}
}

// ⚠ Тело меняется на ОДИН байт. Реализация, сверяющая длину или префикс,
// такую подделку пропустит.
func TestVerifyRejectsTamperedBody(t *testing.T) {
	v := load(t)
	c := v.Cases[0]
	now := time.Unix(c.Timestamp, 0)
	body := []byte(c.RawBody)
	tampered := make([]byte, len(body))
	copy(tampered, body)
	tampered[len(tampered)-2]++ // предпоследний байт: структура JSON цела

	if !webhook.Verify(v.Secret, c.Header, body, now, v.tolerance()) {
		t.Fatal("исходное тело обязано проходить — иначе проверка ниже ничего не значит")
	}
	if webhook.Verify(v.Secret, c.Header, tampered, now, v.tolerance()) {
		t.Fatal("тело, изменённое на один байт, обязано быть отвергнуто")
	}
}

func TestVerifyRejectsForeignSecret(t *testing.T) {
	v := load(t)
	c := v.Cases[0]
	now := time.Unix(c.Timestamp, 0)
	if webhook.Verify("whsec_00000000000000000000000000000000", c.Header, []byte(c.RawBody), now, v.tolerance()) {
		t.Fatal("подпись чужого секрета обязана быть отвергнута")
	}
}

// Защита от воспроизведения перехваченного запроса. Допуск ДВУСТОРОННИЙ:
// метка из будущего — такая же подделка, как из прошлого.
func TestVerifyRejectsStaleTimestamp(t *testing.T) {
	v := load(t)
	c := v.Cases[0]
	tol := v.tolerance()

	cases := []struct {
		name  string
		nowAt int64
		want  bool
	}{
		{"ровно метка", c.Timestamp, true},
		{"на границе допуска", c.Timestamp + int64(v.ToleranceSec), true},
		{"просрочен", c.Timestamp + int64(v.ToleranceSec) + 1, false},
		{"из будущего", c.Timestamp - int64(v.ToleranceSec) - 1, false},
		// ⚠⚠ Метка дальше ~292 лет в будущем: `time.Duration` это int64
		// наносекунд, дальше `Time.Sub` НАСЫЩАЕТСЯ, а модуль насыщенного
		// значения остаётся отрицательным — наивная `abs(now − ts)` отвечает
		// `true` и пропускает подделку свежести.
		//
		// Метку двигать нельзя: она внутри подписанного заголовка. Поэтому в
		// прошлое уезжает `now` — результат тот же, метка оказывается в
		// далёком будущем относительно него.
		//
		// ⚠ Случай значим ТОЛЬКО потому, что свежесть проверяется ДО подписи:
		// иначе наивная реализация вернула бы `false` по несовпадению подписи,
		// и тест был бы зелёным по неправильной причине.
		{"now на 300 лет раньше метки", c.Timestamp - 300*365*86400, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := webhook.Verify(v.Secret, c.Header, []byte(c.RawBody), time.Unix(tc.nowAt, 0), tol)
			if got != tc.want {
				t.Fatalf("Verify = %v, ожидали %v", got, tc.want)
			}
		})
	}
}

// ⚠ Отсутствующий или повреждённый заголовок — обычный ОТКАЗ, а не паника:
// вебхук приходит из сети, и любой недостающий кусок обязан быть отказом.
func TestVerifyRejectsMalformedHeader(t *testing.T) {
	v := load(t)
	c := v.Cases[0]
	now := time.Unix(c.Timestamp, 0)

	bad := []struct {
		name   string
		header string
	}{
		{"пустой", ""},
		{"мусор", "нет тут никакой подписи"},
		{"нет v1", "t=1789948800"},
		{"нет t", "v1=b8c1d43cbb6848fbae42ed4e97f98ff0452a256c50e07b3d70e2cb856a6db428"},
		{"метка не число", "t=вчера,v1=b8c1d43cbb6848fbae42ed4e97f98ff0452a256c50e07b3d70e2cb856a6db428"},
		{"подпись не 64 hex", "t=1789948800,v1=b8c1d43cbb6848fbae42ed4e97f98ff0452a256c50e07b3d70e2cb856a6db42"},
		{"подпись не hex", "t=1789948800,v1=zzc1d43cbb6848fbae42ed4e97f98ff0452a256c50e07b3d70e2cb856a6db428"},
		{"версия не та", "t=1789948800,v2=b8c1d43cbb6848fbae42ed4e97f98ff0452a256c50e07b3d70e2cb856a6db428"},
	}
	for _, b := range bad {
		b := b
		t.Run(b.name, func(t *testing.T) {
			if webhook.Verify(v.Secret, b.header, []byte(c.RawBody), now, v.tolerance()) {
				t.Fatalf("повреждённый заголовок принят: %q", b.header)
			}
		})
	}
}

// Имя заголовка — часть контракта, а не фольклор: вызывающий обязан знать,
// откуда брать подпись, и не переписывать строку руками у себя.
func TestHeaderNameIsExported(t *testing.T) {
	v := load(t)
	if webhook.HeaderName != v.HeaderName {
		t.Fatalf("webhook.HeaderName = %q, вектор называет %q", webhook.HeaderName, v.HeaderName)
	}
}

func TestVectorSetIsIntact(t *testing.T) {
	v := load(t)

	required := []string{"conversion_approved", "payout_paid", "raw_body_signed_verbatim"}
	have := map[string]bool{}
	for _, c := range v.Cases {
		have[c.Name] = true
	}
	for _, name := range required {
		if !have[name] {
			t.Errorf("в наборе пропал случай %q — правило перестало проверяться", name)
		}
	}
	if len(v.Cases) != len(required) {
		t.Errorf("случаев %d, ожидали %d: набор менялся, проверьте что именно", len(v.Cases), len(required))
	}
	if v.ToleranceSec <= 0 {
		t.Error("допуск свежести не задан — проверка повторов была бы пустой")
	}

	// ⚠⚠ В наборе обязано остаться тело НЕ в канонической форме: на нём и
	// только на нём валится реализация, пересобирающая JSON перед проверкой.
	nonCanonical := false
	for _, c := range v.Cases {
		if strings.Contains(c.RawBody, "  ") || strings.ContainsAny(c.RawBody, "абвгдеёжзийклмнопрстуфхцчшщъыьэюя«»") {
			nonCanonical = true
			break
		}
	}
	if !nonCanonical {
		t.Error("в наборе не осталось тела с лишними пробелами или не-ASCII — пересборка JSON перестала ловиться")
	}
}

// В этом пакете сети нет: он считает HMAC над байтами, которые ему дали.
func TestPackageOpensNoTransport(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("не перечислить файлы пакета: %v", err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("не прочитан %s: %v", f, err)
		}
		checked++
		for _, forbidden := range []string{`"net/http"`, `"net"`, `"net/url"`} {
			if strings.Contains(string(src), forbidden) {
				t.Errorf("%s импортирует %s — в пакете проверки подписи сети быть не должно", f, forbidden)
			}
		}
	}
	if checked == 0 {
		t.Fatal("в пакете нет ни одного файла реализации — проверка была бы пустой")
	}
}
