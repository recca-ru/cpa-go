// Граница ядра: метод НЕ открывает свой транспорт и НЕ разбирает ответ сам.
//
// ⚠⚠ Это единственная из четырёх находок пробы WV-0698, которую можно проверить
// машинно, — и проверять её надо именно так. Три остальных («решение об ошибке по
// флагу без взгляда на статус», «чтение тела после декодера», «тело без потолка»)
// закрыты в ядре одной копией и живут в internal/transport. Но если метод обойдёт
// ядро и напишет собственный http.NewRequest, все три вернутся ПО ОДНОЙ НА МЕТОД,
// а оракул останется зелёным: код соберётся, тесты метода пройдут, дефект приедет
// к клиенту.
//
// Семнадцать методов — семнадцать шансов забыть, поэтому это тест, а не пункт в
// чек-листе приёмщика.
package cpa_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// transportOwners — файлы, которым ядро принадлежит по замыслу.
//
// Список именно ПЕРЕЧИСЛЯЕТ разрешённое, а не вычитает запрещённое: знаменатель
// собирается обходом каталога, и новый файл метода попадает под проверку сам, без
// правки этого теста.
var transportOwners = map[string]string{
	"client.go":  "здесь живёт *http.Client из опций потребителя и сборка исполнителя",
	"service.go": "единственная точка сборки запроса и разбора ответа",
}

// Что именно запрещено. Не пакет целиком: http.MethodPost и http.StatusConflict —
// константы контракта, и запрещать их значило бы требовать худшего кода (строковых
// литералов вместо имён).
var forbiddenMembers = map[string][]string{
	"net/http": {
		"NewRequest", "NewRequestWithContext",
		"Get", "Post", "Head", "PostForm",
		"Client", "DefaultClient", "Transport", "DefaultTransport", "RoundTripper",
	},
	// ⚠ Разбор ответа — тот же класс: метод, разобравший data сам, минует
	// проверку обязательных полей, и недостающая сумма станет нулём. Кодирование
	// не запрещено — тело маршалит транспорт, а у типов контракта бывает свой
	// MarshalJSON.
	"encoding/json": {"Unmarshal", "NewDecoder"},
}

func TestMethodsDoNotOpenOwnTransport(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("каталог пакета не прочитан: %v", err)
	}

	fset := token.NewFileSet()
	checked := make([]string, 0, len(entries))
	ownersSeen := make(map[string]bool, len(transportOwners))

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, isOwner := transportOwners[name]; isOwner {
			ownersSeen[name] = true
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s не разобран: %v", name, err)
		}
		checked = append(checked, name)

		// Локальные имена запрещённых пакетов — с учётом псевдонима: импорт
		// под другим именем обошёл бы проверку по тексту молча.
		locals := map[string]string{}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: путь импорта %s не разобран", name, imp.Path.Value)
			}
			if _, forbidden := forbiddenMembers[path]; !forbidden {
				continue
			}
			local := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				local = imp.Name.Name
			}
			locals[local] = path
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			path, ok := locals[pkg.Name]
			if !ok {
				return true
			}
			for _, member := range forbiddenMembers[path] {
				if sel.Sel.Name != member {
					continue
				}
				pos := fset.Position(sel.Pos())
				t.Errorf(
					"%s:%d зовёт %s.%s — метод обходит ядро.\n"+
						"    Запрос собирает request/do в service.go: иначе решение об ошибке,\n"+
						"    чтение тела и потолок размера появятся ещё по одной копии на метод,\n"+
						"    и оракул этого не заметит.",
					pos.Filename, pos.Line, pkg.Name, member,
				)
			}
			return true
		})
	}

	// ⚠⚠ Проверка, которой нечего проверять, зелена ПО ПУСТОТЕ: сломайся обход
	// каталога — и запрет молча перестал бы действовать, оставшись зелёным.
	if len(checked) == 0 {
		t.Fatal("не проверено ни одного файла — обход каталога сломан, запрет не действует")
	}
	for owner := range transportOwners {
		if !ownersSeen[owner] {
			t.Errorf("%s в пакете нет — список владельцев ядра указывает в пустоту", owner)
		}
	}
}

// Вторая половина инварианта: разрешение обязано быть доказано так же, как запрет.
// Владелец ядра назван правильно только если ядро действительно там — иначе
// перечень разрешённого стал бы перечнем случайных имён.
func TestTransportOwnerActuallyHoldsTransport(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(filepath.Join(".", "service.go"))
	if err != nil {
		t.Fatalf("service.go не прочитан: %v", err)
	}
	for _, want := range []string{"transport.Request", "cpatypes.RequireFields"} {
		if !strings.Contains(string(source), want) {
			t.Errorf("в service.go нет %s — ядро живёт не там, где разрешено", want)
		}
	}
}
