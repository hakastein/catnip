# catnip

[![ci](https://github.com/hakastein/catnip/actions/workflows/ci.yml/badge.svg)](https://github.com/hakastein/catnip/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/hakastein/catnip)](https://github.com/hakastein/catnip/releases/latest)

CLI для агентов: просмотр и правка файлов, сцепляемые в bash. `catnip show` печатает файл, диапазон или окрестность
позиции с адресом каждой строки — номером и хешем; `catnip edit` применяет к файлу несколько точных замен и вставок
по адресам строк разом и сначала проверяет все, так что несошедшаяся правка не записывает ничего; `catnip write`
создаёт файл из stdin. После записи catnip запускает фиксеры и проверки из конфига и сообщает результат кодом
выхода. `catnip def` отвечает, где объявлен Go-символ, `catnip ref` — где он используется и кто его реализует,
строками с теми же адресами; `catnip show <путь>::<символ>` печатает объявление Go-символа файла,
`catnip show <путь> --outline` — строки объявлений файла.

Как агенту пользоваться командами — [`skills/catnip/SKILL.md`](skills/catnip/SKILL.md). `catnip help` даёт
синтаксис каждой команды и все правки `edit`, `catnip <команда> --help` или `catnip help <команда>` — все флаги одной
команды; ошибка аргументов печатает справку своей команды.

## Установка

Бинарник — из [последнего релиза](https://github.com/hakastein/catnip/releases/latest): `catnip-<os>-<arch>` для
`linux`, `darwin`, `windows` (`.exe`) на `amd64` и `arm64`, суммы — в `sha256sums.txt`.

```bash
curl -fLo ~/.local/bin/catnip https://github.com/hakastein/catnip/releases/latest/download/catnip-linux-amd64
chmod +x ~/.local/bin/catnip
```

На macOS скачанный бинарник не подписан: `xattr -d com.apple.quarantine catnip` снимает карантин.

Из исходников (Go из `go.mod`):

```bash
make build                                   # bin/catnip, статический, без CGO
install -m 755 bin/catnip ~/.local/bin/catnip
```

Скиллы `skills/catnip` и `skills/catnip-setup` копируются в каталог скиллов агента; по `catnip-setup` агент пишет
`.catnip.json` проекта из его форматтеров и линтеров. Фиксеры и проверки идут через `sh`, на
Windows — из Git for Windows в `PATH`.

## Конфиг

Глобальный — `catnip/config.json` в каталоге конфигов пользователя: `$XDG_CONFIG_HOME` или `~/.config` на Linux,
`~/Library/Application Support` на macOS, `%AppData%` на Windows. Проектный — ближайший к файлу `.catnip.json`
вверх по каталогам; его каталог — корень проекта. Текущий каталог роли не играет. В связанном git worktree, где до
его корня своего `.catnip.json` нет, действует `.catnip.json` основного рабочего дерева для того же относительного
каталога, а корень проекта — этот каталог в worktree: так незакоммиченный конфиг основного дерева работает во всех
его worktree.

```json
{
  "*.go": {"fix": {"goimports": "goimports -w {file}"}, "check": {"go vet": "cd \"$(dirname {file})\" && go vet ."}},
  "*.php": {"check": {"php lint": "php -l {file}"}},
  "src/legacy/*.php": {}
}
```

- Ключ — glob. Glob без `/` сравнивается с именем файла, с `/` — с путём от корня проекта (`*` не переходит
  через `/`).
- `fix` и `check` — команды «имя → команда»; имя catnip печатает в выводе. Фиксер может менять файл: без изменений
  печатает `<имя>: passed`, с изменениями — `<имя>: fixed` (сам diff фиксера — с `--show-fix-diff` перед путём у `edit`
  и `write`). Проверка файл не меняет: `<имя>: passed`
  или `<имя>: failed` и свой вывод; упавший фиксер — тоже `<имя>: failed`. Сначала фиксеры, потом проверки, внутри
  glob по имени; команды идут через `sh -c` в корне проекта с окружением catnip, `{file}` заменяется путём от корня
  проекта в кавычках. Без проектного конфига — в текущем каталоге и путём, как он передан.
- Правило проекта заменяет глобальное правило с тем же glob целиком; пустое правило `{}` отключает его.
- Неизвестное поле, битый JSON или glob, пустое имя или команда, недоступный каталог конфигов — код выхода 2,
  ничего не записано.
- Проектный конфиг исполняется так же, как `Makefile` и тесты репозитория: catnip доверяет ему, как агент доверяет
  `make test` в том же проекте.

## Навигация

`catnip def` отвечает, где объявлен символ Go: по позиции с выражением со строки или по имени. Ответ разрешается по
типам библиотекой [agent-lsp](https://github.com/hakastein/agent-lsp), без сервера и демона.

```bash
catnip def main.go:237 view.ParseTarget    # позиция и выражение со строки
catnip def main.go:237#f032 ParseTarget    # адрес строки из show; строка не должна была измениться
catnip def main.go:236:53 call.Flags       # колонка (в байтах с 1) выбирает одно из одинаковых выражений
catnip def view.Target Navigator.Close     # имена: Func, Type.Method, pkg.Func, pkg.Type.Method
catnip def --body Navigator.Def            # объявление целиком
```

```
main.go:237 view.ParseTarget
internal/view/view.go:29#3f2c: // ParseTarget reads path, path:from-to, path:line, path:line:column (1-based, as positions in LSP tools print them)
internal/view/view.go:30#4a4d: // or path:line#hash, the address catnip prints. A position shows context lines on each side.
internal/view/view.go:31#874a: func ParseTarget(s string, context int) (Target, error) {

main.go:244 fmt.Fprintln
/usr/local/go/src/fmt/print.go:300#2e33: // Fprintln formats using the default formats for its operands and writes to w.
/usr/local/go/src/fmt/print.go:301#d404: // Spaces are always added between operands and a newline is appended.
/usr/local/go/src/fmt/print.go:302#a23d: // It returns the number of bytes written and any write error encountered.
/usr/local/go/src/fmt/print.go:303#0018: func Fprintln(w io.Writer, a ...any) (n int, err error) {
```

- Курсор ставится на последнее слово выражения (`s.store.Write` → `Write`). Выражение должно стоять на строке один
  раз как целое слово; если их несколько, колонка `путь:строка:колонка` выбирает то, что её накрывает. Колонка
  считается в байтах строки с 1.
- Голое имя совпадает с последним сегментом квалифицированных имён; по имени ищутся пакеты проекта, тесты включительно.
- Ответ на каждый вопрос — блок: строка вопроса, затем докблок и сигнатура каждого объявления, с `--body` — объявление
  целиком, каждая строка — `путь:N#hash: текст`. Блоки разделены пустой строкой. Пути — от текущего каталога;
  объявление вне проекта (стандартная библиотека, зависимость из кэша модулей) печатается с абсолютным путём,
  зависимость из `vendor/` — с путём через `vendor/`. Вопрос на самом объявлении отвечает об этом объявлении.
- Адрес `путь:N#hash` из ответа `catnip show` принимает как есть: печатает строку N с окрестностью, а если строка
  изменилась — ту же ошибку с текущей строкой, что `def` и `edit`, и код 1.
- Вопрос без ответа (строка изменилась, выражения на ней нет или оно стоит там несколько раз, объявления нет)
  оставляет строку вопроса, причину с текущей строкой печатает в stderr, остальные вопросы вызова отвечены; код
  выхода — 1.
- Корень Go — ближайший вверх от файла каталог с `go.work`, иначе с `go.mod`; для имени — от текущего каталога. Проект —
  корень и модули его `go.work`, в том числе вне каталога корня (`use ../ext`). Кэш
  индекса — `agent-lsp/cache.sqlite` в каталоге кэша пользователя: первый вызов в модуле загружает пакеты, следующие
  берут их из кэша.

`catnip ref` печатает использования символа Go и, отдельной группой, реализации интерфейса или его метода. Вопросы —
те же, что у `def`, но имя должно называть одно объявление: на несколько catnip печатает в stderr их адреса, и вопрос
задаётся одним из них.

```bash
catnip ref main.go:288 view.ParseTarget          # использования
catnip ref internal/nav/nav.go:31 Definition     # метод интерфейса: использования и реализации
catnip ref internal/view/view.go:34#874a ParseTarget  # адрес из ответа def
catnip ref --limit 3 edit.Hash                   # не больше 3 строк
```

```
internal/nav/nav.go:31 Definition
internal/nav/nav.go:163#d36b: 		if decls, err = ix.Definition(pos); err != nil {
implementations:
/home/u/go/pkg/mod/github.com/hakastein/agent-lsp@v0.5.1/golang/golang.go:243#f93a: func (ix *Index) Definition(pos agentlsp.Position) ([]agentlsp.Declaration, error) {

edit.Hash
internal/edit/apply_test.go:12#4d83: 	return &edit.LineRange{From: from, To: to, FromHash: edit.Hash(fromLine), ToHash: edit.Hash(toLine)}
internal/nav/nav_test.go:114#ae97: 	return fmt.Sprintf("%s:%d#%s: %s\n", filepath.FromSlash(path), n, edit.Hash(text), text)
internal/view/view_test.go:62#76bf: 		{name: "line addresses", target: view.Target{Path: "f", From: 1, To: 2}, gutter: "hash", want: "1#" + edit.Hash("a") + "\ta\n2#" + edit.Hash("b") + "\tb\n"},
21 more lines in 7 files
```

- Ответ — блок на вопрос: строка вопроса, использования, затем после строки `implementations:` реализации. Каждая
  группа — по файлам, файлы по убыванию числа строк, внутри файла — по строкам. Несколько мест на одной строке — одна
  строка.
- `--limit` — сколько строк печатать, по умолчанию 60: сначала по строке на каждый файл (использований, потом
  реализаций), затем остальные по порядку. Группа, у которой строки не поместились, кончается строкой
  `<n> more lines in <m> files`.
- Использования ищутся во всех пакетах корня под linux, darwin и windows, так что `*_windows.go` попадает в ответ и
  на Linux. Объявление само себе не использование; символ без использований — вопрос без ответа (`no uses found`),
  код 1.

`catnip show` открывает объявление Go-файла по имени и показывает объявления файла строками `show` с адресами
`N#hash`, которые берёт `edit`; `--gutter` и `--blame` работают как для файла.

```bash
catnip show internal/view/view.go::ParseTarget   # функция от докблока до конца
catnip show internal/nav/nav.go::Navigator.Def   # метод: Type.Method; поле: Type.Field
catnip show main.go --outline                    # строка каждого объявления файла
```

```
$ catnip show internal/nav/nav.go::Close
35#516c		Close() error

104#75d1	// Close closes the open provider.
105#5fd6	func (n *Navigator) Close() error {
106#53ca		if n.ix == nil {
107#c0de			return nil
108#1493		}
109#7263		return n.ix.Close()
110#b0a4	}
```

- `<путь>::<символ>` печатает каждое объявление файла с этим именем (`Func`, `Type`, `Type.Method`, `Type.Field`;
  голое имя — любое объявление с ним, в том числе метод и поле) от докблока до конца; объявления разделены пустой
  строкой. Объявления с таким именем нет — код 1 и подсказка `--outline`.
- `--outline` печатает строку имени каждого объявления файла по порядку: функции, методы, типы, поля структур и методы
  интерфейсов, переменные и константы; имена с одной строки — одной строкой. С диапазоном или символом — код 2.
- Не-Go файл с `::` или `--outline` — код 1: `no navigation in ".php" files: catnip knows Go (.go)`.

## Коды выхода

| Код | Значение |
|---|---|
| 0 | записано, проверки прошли; `def`, `ref`: каждый вопрос отвечен |
| 1 | ничего не записано: правка не сошлась, файла нет, ошибка ввода-вывода; `def`, `ref`: на вопрос нет ответа; `show`: объявления `<символ>` нет, файл не Go |
| 2 | ошибка аргументов или конфига |
| 3 | записано, но проверка или фиксер упали, либо файл записан не целиком |

## Статистика

Каждый вызов catnip, кроме `catnip stats`, пишется в SQLite-базу `catnip/stats.db` в том же каталоге конфигов
пользователя, что и глобальный конфиг. База локальная, никуда не отправляется. Сбой записи не меняет ни вывод, ни код
выхода.

| Таблица `calls` | |
|---|---|
| `time` | время вызова, UTC, `2026-11-05T12:00:00.000Z` |
| `command` | первый аргумент как есть, в том числе несуществующая команда |
| `flags` | распознанные флаги без повторов через пробел; флаг, которого у catnip нет, виден только в `argv` |
| `argv` | аргументы JSON-массивом; stdin `write` не пишется |
| `exit_code` | код выхода |
| `error` | stderr вызова без текста справки, первый килобайт |
| `error_group` | первая строка `error`, где путь файла, текст в кавычках, адреса `N#hash` и числа заменены на `<file>`, `<text>`, `<n>#<hash>`, `<n>`, а подробности после `; ` или табуляции отброшены |
| `cwd` | рабочий каталог |
| `file` | абсолютный путь файла, `""` без него |
| `project` | корень основного рабочего дерева git-репозитория, вне git — `cwd` |
| `worktree` | корень рабочего дерева с `.git`, `""` вне git |
| `branch` | ветка этого рабочего дерева, `""` при detached HEAD |
| `config` | путь применённого `.catnip.json`, `""` без него |

`project`, `worktree`, `branch` и `config` ищутся от каталога файла, без файла — от `cwd`; для файла вне git `project`,
`worktree` и `branch` ищутся от `cwd`. git не запускается.
Миграций схемы нет: в базу другой схемы catnip ничего не пишет, такую базу удаляют.

Когда база больше 100 МБ, catnip обнуляет `argv` у самой старой десятой части вызовов с кодами 0 и 3; `argv`
упавших вызовов и сами строки остаются.

`catnip stats` поднимает отчёт на `127.0.0.1` со случайным портом, печатает адрес, открывает его в браузере и работает
до Ctrl+C; данные читаются из базы на каждый запрос. Отчёт — дашборд со своими экранами, тёмной и светлой темой и
выбором периода от часа до всего времени: обзор (вызовы, доля ошибок, график вызовов с исходами, проекты с
раскрываемыми рабочими деревьями и каталогами вне git одной группой, первые 20 до «show all», команды, частые ошибки,
файлы), проект с его рабочими деревьями (ветка, свой или основной конфиг), группа ошибок с последним случаем, команда,
файл с историей вызовов, отдельный вызов (команда для повтора, полная ошибка, worktree, ветка, конфиг) и лента вызовов
с отбором по исходу. График считает вызовы по минутам за час, по часам за 24h, по дням за дольше, по часовому поясу
браузера. Сводные таблицы сортируются кликом по заголовку колонки, по умолчанию — по числу вызовов по убыванию; ленты
вызовов идут от новых к старым. Каждый экран — свой адрес, назад и вперёд в браузере работают. Внешних зависимостей у
страницы нет. Остальное —
SQL по базе:

```bash
sqlite3 ~/.config/catnip/stats.db "SELECT value, count(*) FROM calls, json_each(argv)
  WHERE exit_code = 2 AND value LIKE '-%' GROUP BY value ORDER BY 2 DESC"
```

## Ограничения

- Каждый файл пишется атомарно: временный файл и rename. Сохраняются права, включая setuid, setgid и sticky, и
  цель симлинка; симлинк на отсутствующий файл — ошибка. Владелец и расширенные атрибуты не сохраняются. Файл с
  несколькими жёсткими ссылками на Linux и macOS перезаписывается на месте, не атомарно, чтобы новое содержимое
  видели все ссылки: сбой посреди такой записи оставляет файл записанным частично (код 3), а такой файл без права
  записи catnip не перезапишет. На Windows файл с жёсткими ссылками отвязывается от остальных ссылок. На Windows
  из прав есть только атрибут «только для чтения».
- CRLF понимается, только если им кончается каждая строка файла; в файле со смешанными окончаниями текст с `\n`
  совпадает лишь с LF-строками.

## Участие и лицензия

Как предложить изменение — [CONTRIBUTING.md](CONTRIBUTING.md), история выпусков — [CHANGELOG.md](CHANGELOG.md).
Лицензия — [MIT](LICENSE).
