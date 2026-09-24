// Command mutate — мутационный прогон SDK: каждая мутация снимает одно решение и
// обязана уронить тест, который это решение стережёт.
//
// Запуск из корня репозитория:
//
//	go run ./tools/mutate            # все мутации
//	go run ./tools/mutate -run выплат # только те, в имени которых есть подстрока
//
// Тулчейн — GOTOOLCHAIN из окружения, по умолчанию go1.24.13: версия клиента.
//
// # Почему раннер так осторожен с деревом
//
// ⚠⚠ Мутация — это ПРАВКА ИСХОДНИКА. Прогон, прерванный посреди мутации, оставляет
// в дереве сломанный файл, и следующий `git add` увозит его в коммит. Так уже
// было: прогон, оборванный сменой сессии, оставил cpatypes/click.go с лишним
// `cpa:"required"`, и заметили это только по 34 из 35 на следующем прогоне.
//
// Защита в три слоя, и каждый закрывает то, чего не закрывает предыдущий:
//
//  1. defer восстанавливает файл после каждой мутации — обычный выход и паника.
//  2. Обработчик сигнала (Ctrl-C, SIGTERM) восстанавливает и выходит — defer при
//     os.Exit и при сигнале без обработчика не исполняется.
//  3. ЖУРНАЛ в .git/: перед мутацией туда пишется исходное и мутированное
//     содержимое. Жёсткое убийство (SIGKILL, TerminateProcess, закрытая сессия)
//     не перехватывается НИКАКИМ кодом — поэтому следующий запуск первым делом
//     читает журнал и возвращает файл сам. Журнал лежит в .git/, а не в дереве:
//     иначе он сам делал бы дерево грязным.
//
// И поверх всего — отказ стартовать на грязном дереве. Без него восстановление
// «к исходному» означало бы «к исходному вместе с чужой незакоммиченной правкой»,
// а после прогона нельзя было бы проверить, что дерево вернулось чистым.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
)

func main() {
	os.Exit(run())
}

func run() int {
	filter := flag.String("run", "", "только мутации, в имени которых есть эта подстрока")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "⛔", err)
		return 2
	}
	journal := filepath.Join(root, ".git", journalName)

	// Слой 3: сначала — хвост прерванного прогона, и только потом проверка
	// чистоты. В обратном порядке раннер отказался бы стартовать из-за файла,
	// который сам же оставил, и человеку пришлось бы чинить руками то, что
	// раннер умеет вернуть сам.
	restored, err := recoverJournal(root, journal)
	if err != nil {
		fmt.Fprintln(os.Stderr, "⛔ прерванный прогон не восстановлен:", err)
		return 2
	}
	if restored != "" {
		fmt.Printf("↩ восстановлен файл после прерванного прогона: %s\n\n", restored)
	}

	if dirty, err := dirtyFiles(root); err != nil {
		fmt.Fprintln(os.Stderr, "⛔", err)
		return 2
	} else if len(dirty) > 0 {
		fmt.Fprintln(os.Stderr, "⛔ дерево не чистое — мутации не запускаю. Незакоммиченное:")
		for _, line := range dirty {
			fmt.Fprintln(os.Stderr, "   ", line)
		}
		fmt.Fprintln(os.Stderr, "Закоммитьте или отложите (git stash -u) и повторите.")
		return 2
	}

	// Слой 2: сигнал. Текущая мутация известна через active; обработчик
	// восстанавливает её и выходит, не дожидаясь go test.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	var active atomic.Pointer[applied]
	go func() {
		s := <-sig
		if a := active.Load(); a != nil {
			if err := a.revert(); err != nil {
				fmt.Fprintf(os.Stderr, "\n⛔ сигнал %v: файл %s НЕ восстановлен: %v — восстановит следующий запуск по журналу\n", s, a.path, err)
				os.Exit(130)
			}
			_ = os.Remove(journal)
			fmt.Fprintf(os.Stderr, "\n↩ сигнал %v: восстановлен %s\n", s, a.path)
		}
		os.Exit(130)
	}()

	toolchain := os.Getenv("GOTOOLCHAIN")
	if toolchain == "" {
		toolchain = "go1.24.13"
	}

	var total, ok int
	for _, m := range catalog {
		if *filter != "" && !strings.Contains(m.Name, *filter) {
			continue
		}
		total++
		verdict, detail := runOne(root, journal, toolchain, m, &active)
		fmt.Println(verdict)
		for _, line := range detail {
			fmt.Println("    ", line)
		}
		if strings.HasPrefix(verdict, "🔴 красная:") || strings.HasPrefix(verdict, "🟢 зелёная, как и должна:") {
			ok++
		}
	}
	if total == 0 {
		fmt.Fprintf(os.Stderr, "⛔ под фильтр %q не попала ни одна мутация — «ничего не запущено» не должно читаться как «всё красное»\n", *filter)
		return 2
	}

	// Дерево обязано вернуться ровно таким, каким было: иначе прогон оставил
	// что-то после себя, и это надо увидеть сейчас, а не в чужом коммите.
	if dirty, err := dirtyFiles(root); err != nil || len(dirty) > 0 {
		fmt.Fprintln(os.Stderr, "\n⛔ после прогона дерево НЕ чистое:", dirty, err)
		return 1
	}

	fmt.Printf("\nведёт себя как объявлено: %d из %d\n", ok, total)
	if ok != total {
		return 1
	}
	return 0
}

// runOne применяет мутацию, гонит тесты и возвращает вердикт.
func runOne(root, journal, toolchain string, m Mutation, active *atomic.Pointer[applied]) (string, []string) {
	a, err := apply(root, journal, m)
	if err != nil {
		return "⛔ " + m.Name + ": " + err.Error(), nil
	}
	active.Store(a)
	// Слой 1.
	defer func() {
		active.Store(nil)
		if err := a.revert(); err != nil {
			fmt.Fprintf(os.Stderr, "⛔ %s НЕ восстановлен: %v — восстановит следующий запуск по журналу\n", a.path, err)
			return
		}
		_ = os.Remove(journal)
	}()

	cmd := exec.Command("go", "test", "-count=1", "-run", m.Pattern, m.Pkg)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN="+toolchain)
	out, runErr := cmd.CombinedOutput()
	text := string(out)
	red := runErr != nil

	switch {
	case red && brokenBuild(text):
		return "⚠ КРАСНАЯ ПО СБОРКЕ (не считается): " + m.Name, firstLines(text, 3)
	case red && m.WantRed:
		return "🔴 красная: " + m.Name, failLines(text, 2)
	case !red && !m.WantRed:
		return "🟢 зелёная, как и должна: " + m.Name, nil
	case red:
		return "🔴 КРАСНАЯ (плохо, требует худшего кода): " + m.Name, failLines(text, 3)
	default:
		return "🟢 ЗЕЛЁНАЯ (плохо): " + m.Name, nil
	}
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errors.New("не внутри git-репозитория: раннеру нужны git status и .git/ для журнала")
	}
	return strings.TrimSpace(string(out)), nil
}

// dirtyFiles — строки `git status --porcelain`; пусто значит чистое дерево.
func dirtyFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status не отработал: %w", err)
	}
	return parsePorcelain(string(out)), nil
}

func parsePorcelain(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, "\r"))
		}
	}
	return lines
}

func brokenBuild(out string) bool {
	for _, marker := range []string{
		"[build failed]", "syntax error", "undefined:", "cannot use", "declared and not used",
	} {
		if strings.Contains(out, marker) {
			return true
		}
	}
	return false
}

func failLines(out string, n int) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- FAIL") {
			lines = append(lines, line)
			if len(lines) == n {
				break
			}
		}
	}
	return lines
}

func firstLines(out string, n int) []string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines
}
