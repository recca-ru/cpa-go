// Пакет `postback` — подпись и проверка S2S-постбэка CPA.
//
// Эти тесты написаны ДО реализации и падают, пока её нет. Они же являются
// точным описанием того, что нужно сделать: расхождение на один символ
// канонической строки красит тест.
//
// ⚠⚠ Векторы в `testdata/signature_vectors.json` порождены ЖИВЫМИ функциями
// платформы Recca. Если реализация расходится с вектором — неправа реализация.
// Правка `testdata/` запрещена приёмкой диффа.
package postback_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.recca.ru/cpa/postback"
)

const vectorsPath = "../testdata/signature_vectors.json"

// Поля вектора. Каждое — указатель, потому что вектор различает «ключа не
// было» и «ключ был со значением», а канон решает именно ПРИСУТСТВИЕМ.
//
// ⚠ `sum_rub` читается как `json.Number`, а не как число: в канон уходит
// ЛЕКСИЧЕСКАЯ форма, и `12.5` обязана остаться `12.5`, а не стать `12.50`.
// Разбор во float с последующим форматированием ломает подпись молча.
type vectorFields struct {
	ClickID    *string      `json:"click_id"`
	ExternalID *string      `json:"external_id"`
	Status     *string      `json:"status"`
	SumRUB     *json.Number `json:"sum_rub"`
	SubID      *string      `json:"sub_id"`
	Goal       *string      `json:"goal"`
	Timestamp  *string      `json:"timestamp"`
}

type sigCase struct {
	Name       string       `json:"name"`
	What       string       `json:"what"`
	KeyID      string       `json:"key_id"`
	Fields     vectorFields `json:"fields"`
	Canonical  string       `json:"canonical"`
	Segments   int          `json:"segments"`
	Signature  string       `json:"signature"`
	MustVerify bool         `json:"must_verify"`
	Rejection  *string      `json:"rejection"`
}

type secureTokenCase struct {
	Name    string `json:"name"`
	KeyID   string `json:"key_id"`
	OfferID string `json:"offer_id"`
	Token   string `json:"token"`
}

type sigVectors struct {
	ContractVersion string `json:"contract_version"`
	Secrets         struct {
		Current  string `json:"current"`
		Previous string `json:"previous"`
	} `json:"secrets"`
	Cases        []sigCase         `json:"cases"`
	SecureTokens []secureTokenCase `json:"secure_tokens"`
}

func load(t *testing.T) sigVectors {
	t.Helper()
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("векторы не прочитаны (%s): %v", vectorsPath, err)
	}
	var v sigVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("векторы не разобраны: %v", err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("в векторах нет ни одного случая — проверять нечего")
	}
	return v
}

func secretFor(t *testing.T, v sigVectors, keyID string) string {
	t.Helper()
	switch keyID {
	case "current":
		return v.Secrets.Current
	case "previous":
		return v.Secrets.Previous
	}
	t.Fatalf("неизвестный слот секрета: %q", keyID)
	return ""
}

// Перекладывает поля вектора в поля пакета. Именно эта функция и объявляет,
// какой формы обязана быть `postback.Fields`.
func toFields(vf vectorFields) postback.Fields {
	f := postback.Fields{}
	if vf.ClickID != nil {
		f.ClickID = *vf.ClickID
	}
	if vf.ExternalID != nil {
		f.ExternalID = *vf.ExternalID
	}
	if vf.Status != nil {
		f.Status = *vf.Status
	}
	if vf.SubID != nil {
		f.SubID = *vf.SubID
	}
	if vf.SumRUB != nil {
		s := vf.SumRUB.String()
		f.SumRUB = &s
	}
	if vf.Goal != nil {
		s := *vf.Goal
		f.Goal = &s
	}
	if vf.Timestamp != nil {
		s := *vf.Timestamp
		f.Timestamp = &s
	}
	return f
}

func TestCanonicalMatchesVectors(t *testing.T) {
	v := load(t)
	for _, c := range v.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			got := postback.Canonicalize(toFields(c.Fields))
			if got != c.Canonical {
				t.Fatalf("канон разошёлся\n  ожидали: %q\n  получили: %q\n  случай: %s", c.Canonical, got, c.What)
			}
			// Число сегментов однозначно называет форму: разделителей всегда
			// на один меньше, чем сегментов, и укорачивать строку нельзя.
			if n := strings.Count(got, ":") + 1; n != c.Segments {
				t.Fatalf("сегментов %d, ожидали %d — форма канона не та", n, c.Segments)
			}
		})
	}
}

func TestSignMatchesVectors(t *testing.T) {
	v := load(t)
	for _, c := range v.Cases {
		c := c
		if !c.MustVerify {
			continue // у отрицательных подпись намеренно не та
		}
		t.Run(c.Name, func(t *testing.T) {
			got := postback.Sign(toFields(c.Fields), secretFor(t, v, c.KeyID))
			if got != c.Signature {
				t.Fatalf("подпись разошлась\n  ожидали: %s\n  получили: %s", c.Signature, got)
			}
			if got != strings.ToLower(got) {
				t.Fatalf("подпись обязана быть в НИЖНЕМ регистре: %s", got)
			}
		})
	}
}

func TestVerifyAcceptsValidAndRejectsInvalid(t *testing.T) {
	v := load(t)
	for _, c := range v.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			ok := postback.Verify(toFields(c.Fields), secretFor(t, v, c.KeyID), c.Signature)
			if ok != c.MustVerify {
				verdict := "отвергнуть"
				if c.MustVerify {
					verdict = "принять"
				}
				t.Fatalf("Verify обязан %s этот случай, вернул %v\n  почему: %s", verdict, ok, c.What)
			}
		})
	}
}

// ⚠⚠ Отдельно от таблицы: подпись, сделанная ПРЕЖНИМ секретом, не должна
// сходиться с ДЕЙСТВУЮЩИМ. Без этой проверки `Verify`, возвращающий true на
// любую подпись верной формы, прошёл бы все положительные векторы целиком.
func TestSignatureOfOtherSlotDoesNotVerify(t *testing.T) {
	v := load(t)
	var rotated *sigCase
	for i := range v.Cases {
		if v.Cases[i].KeyID == "previous" && v.Cases[i].MustVerify {
			rotated = &v.Cases[i]
			break
		}
	}
	if rotated == nil {
		t.Fatal("в векторах нет случая с прежним секретом — проверка была бы пустой")
	}
	f := toFields(rotated.Fields)
	if !postback.Verify(f, v.Secrets.Previous, rotated.Signature) {
		t.Fatal("подпись прежнего слота обязана сходиться со СВОИМ секретом")
	}
	if postback.Verify(f, v.Secrets.Current, rotated.Signature) {
		t.Fatal("подпись прежнего слота НЕ должна сходиться с действующим секретом")
	}
}

func TestSecureTokenMatchesVectors(t *testing.T) {
	v := load(t)
	if len(v.SecureTokens) == 0 {
		t.Fatal("в векторах нет токенов GET-двери")
	}
	for _, tc := range v.SecureTokens {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			got := postback.SecureToken(secretFor(t, v, tc.KeyID), tc.OfferID)
			if got != tc.Token {
				t.Fatalf("токен разошёлся\n  ожидали: %s\n  получили: %s", tc.Token, got)
			}
			// Префикс отличает токен от самого секрета: боевой секрет
			// начинается с `cpaps_`, и по первым знакам их видно в журнале.
			if !strings.HasPrefix(got, "cpag_") {
				t.Fatalf("токен обязан начинаться с cpag_, получили %s", got)
			}
		})
	}
}

func TestCheckFreshness(t *testing.T) {
	const base = int64(1789948800)
	now := time.Unix(base, 0)
	tol := 300 * time.Second

	cases := []struct {
		name string
		ts   int64
		want bool
	}{
		{"ровно сейчас", base, true},
		{"на границе допуска в прошлом", base - 300, true},
		{"на границе допуска в будущем", base + 300, true},
		{"за границей в прошлом", base - 301, false},
		// ⚠ Будущее проверяется ТОЖЕ: допуск двусторонний. Реализация,
		// сравнивающая только `now - ts`, пропустит метку из будущего, а
		// это ровно то, чем подделывают свежесть.
		{"за границей в будущем", base + 301, false},
		// ⚠⚠ Метка В МИЛЛИСЕКУНДАХ — самая частая ошибка интеграции, и самая
		// опасная форма «будущего». `time.Duration` это int64 наносекунд, то
		// есть потолок ~292 года; дальше `Time.Sub` НАСЫЩАЕТСЯ, а модуль
		// насыщенного значения остаётся отрицательным (−minInt64 == minInt64).
		// Поэтому наивная `abs(now.Sub(ts)) <= tolerance` отвечает на такую
		// метку `true` — пропускает подделку свежести, выглядя при этом
		// очевидно правильной.
		//
		// Случай добавлен ПОСЛЕ приёмки: дефект нашло ревью контура, а не этот
		// тест, и без закрепления следующая «оптимизация» вернула бы разность
		// длительностей молча. Проверено опытом 21.09.2026: наивная форма
		// ошибается здесь и на «годе 9999», верная — ни на одном из семи.
		{"метка в миллисекундах", base * 1000, false},
		{"год 9999", 253402300799, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := postback.CheckFreshness(c.ts, now, tol); got != c.want {
				t.Fatalf("CheckFreshness(%d) = %v, ожидали %v", c.ts, got, c.want)
			}
		})
	}
}

// Список случаев живёт ЗДЕСЬ, отдельно от генератора векторов. Иначе удаление
// случая из генератора оставило бы набор зелёным, а покрытие правила молча
// исчезло бы (урок WV-0751).
func TestVectorSetIsIntact(t *testing.T) {
	v := load(t)

	required := []string{
		"legacy_five_segments",
		"sum_absent",
		"sum_zero",
		"sum_fractional",
		"goal_empty_six_segments",
		"goal_named_six_segments",
		"timestamp_forces_seven_goal_absent",
		"timestamp_and_goal_seven",
		"previous_slot_rotation",
		"tampered_signature",
		"signature_not_64_hex",
	}
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

	negatives := 0
	for _, c := range v.Cases {
		if !c.MustVerify {
			negatives++
		}
	}
	// Набор из одних положительных зелен на `Verify`, возвращающем true всегда.
	if negatives < 2 {
		t.Errorf("отрицательных случаев %d — их обязано быть не меньше двух", negatives)
	}
}

// ⚠ В этом пакете сети нет ВОВСЕ: он считает подписи над строками. Транспорт,
// открытый здесь, означал бы, что подпись начали проверять по дороге, а не до
// доверия содержимому. Проверка машинная, потому что читать это глазами на
// каждом ревью — то же самое, что не проверять.
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
				t.Errorf("%s импортирует %s — в пакете подписи сети быть не должно", f, forbidden)
			}
		}
	}
	if checked == 0 {
		t.Fatal("в пакете нет ни одного файла реализации — проверка была бы пустой")
	}
}
