package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Каталог сверяется с деревом: у каждой мутации якорь встречается РОВНО один
// раз. Иначе правка кода молча выводит мутацию из строя — раннер скажет
// «якорь не найден», а читатель итога увидит «34 из 35» и решит, что упал тест.
func TestCatalogAnchorsExistExactlyOnce(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, m := range catalog {
		path := filepath.Join(root, filepath.FromSlash(m.File))
		if m.Create {
			if _, err := os.Stat(path); err == nil {
				t.Errorf("%s: файл-зонд %s уже существует в дереве", m.Name, m.File)
			}
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", m.Name, err)
			continue
		}
		if n := strings.Count(string(raw), m.Old); n != 1 {
			t.Errorf("%s: якорь в %s встречается %d раз, ждали 1", m.Name, m.File, n)
		}
	}
	if len(catalog) == 0 {
		t.Fatal("каталог пуст — «ничего не проверено» не должно быть зелёным")
	}
}

func setup(t *testing.T, content string) (root, journal, path string) {
	t.Helper()
	root = t.TempDir()
	journal = filepath.Join(root, journalName)
	path = filepath.Join(root, "f.go")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, journal, path
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Главный случай: процесс убит, мутация стоит в дереве. Следующий запуск обязан
// вернуть исходник и снять журнал.
func TestRecoverRevertsMutationLeftByKilledRun(t *testing.T) {
	root, journal, path := setup(t, "package p // original\n")
	if _, err := apply(root, journal, Mutation{File: "f.go", Old: "original", New: "mutated"}); err != nil {
		t.Fatal(err)
	}
	// «Убийство»: ни defer, ни revert не исполнились.
	if got := read(t, path); !strings.Contains(got, "mutated") {
		t.Fatalf("мутация не применена: %q", got)
	}

	restored, err := recoverJournal(root, journal)
	if err != nil {
		t.Fatal(err)
	}
	if restored != "f.go" {
		t.Errorf("restored = %q", restored)
	}
	if got := read(t, path); got != "package p // original\n" {
		t.Errorf("файл не восстановлен: %q", got)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Error("журнал остался после восстановления")
	}
}

// Убит между журналом и записью мутации: файл нетронут, журнал просто снимается.
func TestRecoverWhenMutationNeverLanded(t *testing.T) {
	root, journal, path := setup(t, "package p // original\n")
	if err := writeJournal(journal, record{
		Path: "f.go", Existed: true,
		Original: []byte("package p // original\n"), Mutated: []byte("package p // mutated\n"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := recoverJournal(root, journal); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "package p // original\n" {
		t.Errorf("файл тронут: %q", got)
	}
}

// ⚠ Файл изменён ПОСЛЕ обрыва — это чья-то работа. Затирать её исходником нельзя:
// стоп, файл и журнал остаются как есть.
func TestRecoverRefusesToOverwriteLaterEdits(t *testing.T) {
	root, journal, path := setup(t, "package p // original\n")
	if _, err := apply(root, journal, Mutation{File: "f.go", Old: "original", New: "mutated"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package p // human edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := recoverJournal(root, journal); err == nil {
		t.Fatal("чужая правка затёрта молча")
	}
	if got := read(t, path); got != "package p // human edit\n" {
		t.Errorf("чужая правка потеряна: %q", got)
	}
	if _, err := os.Stat(journal); err != nil {
		t.Error("журнал снят, хотя восстановление отказано — исходник потерян")
	}
}

// Файл-зонд, оставленный убитым прогоном, удаляется.
func TestRecoverRemovesProbeFile(t *testing.T) {
	root, journal, path := setup(t, "")
	if _, err := apply(root, journal, Mutation{File: "f.go", Create: true, New: "package p\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := recoverJournal(root, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("зонд остался в дереве")
	}
}

// Без журнала восстановление — пустая операция, а не ошибка.
func TestRecoverWithoutJournalIsNoop(t *testing.T) {
	root, journal, _ := setup(t, "package p\n")
	restored, err := recoverJournal(root, journal)
	if err != nil || restored != "" {
		t.Errorf("restored = %q, err = %v", restored, err)
	}
}

// Обычный путь: revert возвращает исходник.
func TestRevertRestoresOriginal(t *testing.T) {
	root, journal, path := setup(t, "package p // original\n")
	a, err := apply(root, journal, Mutation{File: "f.go", Old: "original", New: "mutated"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.revert(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "package p // original\n" {
		t.Errorf("не восстановлен: %q", got)
	}
}

// Якорь не найден или неоднозначен — мутация не применяется, журнал не пишется.
func TestApplyRefusesAmbiguousAnchor(t *testing.T) {
	root, journal, path := setup(t, "package p // x x\n")
	if _, err := apply(root, journal, Mutation{File: "f.go", Old: "x", New: "y"}); err == nil {
		t.Fatal("двусмысленный якорь применён")
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Error("журнал записан для неприменённой мутации")
	}
	if got := read(t, path); got != "package p // x x\n" {
		t.Errorf("файл тронут: %q", got)
	}
}

func TestParsePorcelain(t *testing.T) {
	got := parsePorcelain(" M cpatypes/click.go\r\n?? zz_probe_boundary.go\n\n")
	if len(got) != 2 || got[0] != " M cpatypes/click.go" || got[1] != "?? zz_probe_boundary.go" {
		t.Errorf("parsePorcelain = %q", got)
	}
	if got := parsePorcelain(""); len(got) != 0 {
		t.Errorf("чистое дерево дало %q", got)
	}
}
