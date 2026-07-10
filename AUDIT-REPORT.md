# node-stats — Аудит архитектуры, синка и чистоты данных

Дата: 2026-07-10. Метод: 7 параллельных аудиторов по подсистемам (Raft/консенсус, репликация метрик, bridge/uplink, конфиг-синк, гигиена данных, безопасность, качество кода) + сборка/тесты + адверсариальная верификация каждой находки medium+. Все 14 верифицированных находок подтверждены. Build/vet/test — зелёные.

---

## 0. Вердикт по твоей модели архитектуры

| Твоё утверждение | Вердикт | Реальность |
|---|---|---|
| Локальный кластер держит под консенсусом только необходимый минимум | **частично** | Под Raft: hosts, users, config, auth-secrets, join-токены, peer-каталог, **коннекторы**. Проблема: под консенсусом сидят и user-mutations, которые НЕ реплицируются целиком (delete/refresh-token — см. F1/F2), т.е. «минимум» местами дырявый, а не избыточный. |
| Метрики не под консенсусом, синкуются best-effort с потерями | **держится** | Метрики полностью сняты с Raft-лога и едут по off-Raft HMAC-P2P (`internal/cluster/raft/metricstream`). Fire-and-forget, дроп при сбое, восстановление на следующем тике. Именно как ты хотел. |
| Все конфиги синкуются в локальном кластере | **частично** | Синкаются: коннекторы, auth-secrets, bridge-config, peer URLs. НЕ синкаются (осознанно, node-local в `.env`): `AUTO_UPDATE`, release-channel, deploy-webhook, `TRAEFIK/NGINX_DIR`. Это ок. НО user-delete и refresh-токены — это состояние, которое пользователь ждёт синхронным, а оно не едет (F1/F2). |
| Каждая нода знает набор URL паблик-кластера и сама переключается по доступности | **частично** | Есть `Picker` (`bridge/picker.go`) — health-пробы `GET /raft/ping`, EMA RTT, выбор лучшего. НО: (а) шлёт метрики каждая нода, а топологию — только лидер; (б) health меряется только по ping, а не по успеху реального `/raft/bridge/replicate` — нода-приёмник, отвечающая на ping, но отвергающая батчи, залипает навсегда (F11); (в) метрик-стример шлёт только на **seed-адреса**, каталог хаба не использует (F10). |
| Клауд-кластер тоже синкует все данные внутри себя, консенсус только на минимум | **частично** | Внутри хаба — тот же Raft (control-plane) + off-Raft метрики. НО принятый от спока батч метрик пишется только в БД **той ноды хаба, что его приняла**, и не рефанится другим нодам хаба → разные ноды хаба показывают разные данные одного хоста (F10). |
| Каждая нода реплицирует в свою базу весь доступный ей батч данных | **частично** | Да для control-plane (Raft snapshot + лог). Для метрик — только текущий тик; **историю дропнутый батч не лечит** (F8), плюс MAC-коллизии docker-bridge молча теряют/сливают потоки (F7). |
| Данные чистые, нигде не застревают | **частично** | В основном да, но есть залипания: ghost-хосты на хабе после аутэйджа (F12), offline-машины мигают online каждые 10 мин (F9), PBS `remoteStatus` не чистится никогда (F13), join-токены копятся вечно (F16). |

---

## 1. Критичные и высокие находки

### F1 — 🔴 HIGH (security): удаление пользователя не реплицируется
`internal/auth/users/service.go:281`. Интерфейс `RaftReplicator` (service.go:74-78) имеет только `SubmitUserUpsert` и `SubmitUserPasswordChange`. `SubmitUserDelete` **не существует нигде** (проверено grep'ом), хотя `CmdUserDelete` и его applier зарегистрированы (appliers.go:79, 404-412) — их некому продьюсить.
**Последствие:** админ удаляет юзера на node1 → на node2/node3 строка `users` жива. JWT-secret общий на кластер → удалённый юзер логинится и работает на любой другой ноде кластера. Полноценная дыра авторизации в кластере.
**Фикс:** добавить `SubmitUserDelete` в интерфейс + вызвать в `userService.Delete`; applier уже готов.

### F2 — 🔴 HIGH→MEDIUM (correctness/security): refresh-токены не реплицируются
`internal/auth/users/repository.go:122`, `service.go:610-637`. Issue/revoke пишут только в локальную БД. `CmdRefreshTokenIssue/Revoke` есть в appliers, но продьюсеров нет.
**Последствие:** (а) `ValidateRefreshToken` бьёт по локальной БД (`FindByJTI`) → refresh, выданный node1, на node2 не находится → юзера разлогинивает при переключении ноды. Docs (`CLUSTER.md:215`) прямо обещают «seamless refresh on any node» — контракт нарушен. (б) logout/revoke на node1 не доезжает до node2 → отозванный токен там ещё живой.
**Фикс:** реплицировать issue/revoke (команды готовы).

### F3 — 🔴 HIGH (correctness): cipher коннекторов не перевыпускается после runtime-join
`internal/app/server/server.go:217`, `connectors/crypto.go:16-23`. `NewCipher(cfg.JWTSecret)` строится один раз на старте из boot-secret. При join через мастер нода получает настоящий cluster-JWT-secret позже (placeholder→real swap), но `Cipher` — иммутабельный `[32]byte`, метода rekey нет.
**Последствие:** секрет коннектора, зашифрованный под placeholder до подхвата настоящего ключа, становится нечитаемым навсегда; коммит b98622b (self-heal) лечит только следствие, не причину.
**Фикс:** сделать ключ cipher swap-able так же, как HMAC-secret у metricstream (там `SetIntraSecret` уже есть — тот же паттерн).

### F4 — 🟠 MEDIUM (security): `/raft/forward` без аутентификации
`internal/cluster/raft/handler.go:388`, `server.go:754-755`. Роут смонтирован на **публичной** группе `api` только с rate-limiter'ом (50/s), без `AuthJWT`/`RequireAdmin`/HMAC — в отличие от `/raft/bridge/replicate` (HMAC) и всех admin raft-роутов. `Forward()` биндит произвольный `Command` и сабмитит его в Raft-лог.
**Последствие:** кто угодно с сетевым доступом к ноде (LAN, а при интернет-экспозиции — снаружи) может форвардить любые control-plane команды на лидера: upsert юзера-админа, подмена auth-secret, upsert хостов. Полный обход авторизации кластера.
**Фикс:** HMAC под cluster-JWT-secret на `/raft/forward` (тот же паттерн, что у metricstream), либо mTLS/сетевая изоляция Raft-порта.

### F5 — 🟠 MEDIUM (correctness): FSM.Apply молча расходится с пирами при ошибке записи
`internal/cluster/raft/fsm.go:156`. Контракт (fsm.go:21-24, commands.go:92-94) требует паниковать на недетерминированных ошибках, чтобы нода упала и пересинкалась из снапшота. Ни один applier не паникует — все возвращают ошибку репозитория, а `applierCtx` с 10s-дедлайном (при busy_timeout 30s) превращает залоченный SQLite в тихий `context deadline exceeded`.
**Последствие:** запись, применённая на пирах, но упавшая тут по таймауту/локу, тихо теряется на этой ноде → её SQLite расходится с кластером без сигнала. «Данные застревают» именно так.
**Фикс:** для недетерминированных ошибок (lock/disk) — паника (как задумано контрактом), для детерминированных — вернуть ошибку.

### F6 — 🟠 MEDIUM (correctness): MAC-коллизии docker-bridge теряют/сливают потоки метрик
`internal/cluster/raft/metricsink.go:83`, `collector.go:209-213`. Идентичность хоста в кластере — MAC docker-bridge; на дефолтном docker-инсталле разные машины дают одинаковый MAC. Есть guard против слияния с id=1, но между двумя *удалёнными* хостами с одинаковым MAC — резолв по MAC вернёт чужую строку.
**Последствие:** метрики хоста B пишутся под хост A или дропаются. Данные «застревают»/пропадают на ровном месте в типичной docker-конфигурации.
**Фикс:** предпочитать `HardwareUUID`/`machine-id`/`external_id` над bridge-MAC при резолве (частично уже есть для connector-хостов — распространить).

### F7 — 🟠 MEDIUM (correctness): хаб из нескольких нод отдаёт расходящиеся данные
`internal/cluster/raft/metricsink.go:78`. Принятый батч метрик пишется только в БД принявшей ноды + её SSE; рефанаута другим нодам хаба нет. А спок шлёт только на seed-URL.
**Последствие:** нода хаба, что приняла батч, знает свежие метрики; остальные — нет. Клиент, попавший на другую ноду хаба, видит устаревшие/пустые данные. Твоя модель «хаб синкует всё внутри себя» для метрик не выполняется.
**Фикс:** либо рефанаут принятого off-Raft батча остальным нодам хаба, либо спок шлёт всем нодам хаба (через каталог, не только seeds).

---

## 2. Средние и низкие находки (синк / чистота)

- **F8 — MEDIUM (data-loss):** дропнутый батч метрик лечится только *текущим состоянием*, не историей (`metricstream/sender.go:6`). «Next full-state resend» восстанавливает лишь последний тик — дырка в графике остаётся навсегда. Для best-effort метрик это приемлемо, но стоит зафиксировать в доках как поведение.
- **F9 — MEDIUM (correctness):** bridge-reconcile каждые 10 мин бампает `last_seen` ВСЕХ хостов (`replicator.go:237` → `BackfillLocalHosts` без фильтра свежести → `UpsertHost` ставит `LastSeen=now`). Офлайн-машины периодически мигают «online» по всему кластеру/хабу.
- **F10 — MEDIUM (correctness):** picker никогда не демоутит ping-healthy URL, который стабильно отвергает `/raft/bridge/replicate` (`picker.go:289`). Health = только ping; реальный успех шипа не учитывается → залипание на «здоровой», но отвергающей ноде.
- **F11 — MEDIUM (data-loss):** host-delete, потерянный во время аутэйджа хаба, никогда не переотправляется (`bridge/sender.go:231`, буфер in-memory cap=200, drop-oldest, теряется при рестарте/смене лидера) → хаб копит ghost-хосты навсегда.
- **F12 — MEDIUM (perf):** цикл сбора синхронно блокируется на Raft-submit при потере кворума/недоступности лидера (`hosts/service.go:172`, 5s Raft + 7s HTTP-форвард inline на hot-path). Верификатор снизил до low — метрики свои нода всё равно пишет, но тик тормозит.

---

## 3. Гигиена данных — где копится/застревает

- **F13 — MEDIUM (leak):** PBS `remoteStatus` map (`pbs/poller.go:110`) не эвиктится и без TTL — снапшоты удалённых хостов живут вечно, медленный рост RSS на пирах/хабе. Нарушает конвенцию «keep resident memory low».
- **F14 — LOW:** `cluster_join_tokens` (`raft/entities.go:39`) — consumed/expired токены не удаляются никогда, копятся и едут в каждом снапшоте.
- **F15 — LOW (perf):** ретеншн метрик жёстко ограничен 2000 строк/таблица за 2 мин (`retention/service.go:111`) — большой кластер/хаб обгоняет чистку, таблицы растут.
- **F16 — INFO (leak):** per-host in-memory карты (`docker lastSig`, PBS `upsertState`) не дропают записи удалённых хостов/коннекторов.
- **F17 — INFO (perf):** boot-time PK-rebuild метрик-таблиц (`metric_pk_migration.go:292`) может застопорить старт на минуты и удвоить диск на больших SQLite при апгрейде.

Хорошее: ретеншн 5 метрик-таблиц + refresh-токенов + app-icon кэша покрыт; host cascade-delete чанкается (5000); `docker_container_entities` — current-state, prune-on-write; SSE-брокер дропает медленных клиентов неблокирующе (`broker.go:56`); Raft-снапшоты метрики исключают (лог не пухнет).

---

## 4. Красота и безопасность кода

**Оценка структуры: хорошо.** Реальная раскладка (`internal/metrics/*`, `internal/auth/*`, `internal/platform/*`, `internal/cluster/*`, плоские collector/entities/handler/repository/service.go) чище церемониального 3-слойного паттерна из `ARCHITECTURE.md`.

- **🔴 HIGH (docs-drift):** `ARCHITECTURE.md` и `docs/CLUSTER.md` описывают несуществующую архитектуру: (а) модульный паттерн `internal/modules/{name}/...` и пути repo-интерфейсов — все неверны; (б) метрики «едут через Raft как CmdMetricBatch, в снапшоте, joiner получает всю историю» — код давно перевёл их на off-Raft HTTP-стрим, из снапшота исключил. Это главный источник путаницы. Обнови доки.
- **MEDIUM:** `internal/app/database/dialect.go` — байт-в-байт дубликат `internal/app/dbutil/dialect.go` (мёртвый), причём доки ссылаются на мёртвую копию.
- **MEDIUM:** `di/container.go` (1492 стр.) —半 DI, 半 Raft-lifecycle стейт-машина; `server.go` (~390 стр. admin-settings бизнес-логики инлайн в 16 анонимных хендлерах). Сиймы для распила: вынести admin-settings хендлеры в отдельный handler-пакет; вынести Raft-lifecycle из Container.
- **MEDIUM (test-coverage):** непокрыты самые рискованные места — Raft appliers, setup/join handler, controller sidecar, docker collector. 24 пакета с тестами зелёные, но ядро консенсуса без юнитов.
- **MEDIUM:** непоследовательные ошибки — `apperror`/ErrorHandler есть, но им пользуется только auth-handler; остальные руками лепят `gin.H{"error":...}`.
- **MEDIUM:** фронт — red/amber threshold-хексы продублированы в 6+ файлах вопреки конвенции `chartColors.ts`; главный JS-чанк 1.4 MB (>500 kB warning Vite).
- **LOW:** 13 Go-файлов не проходят `gofmt` (напр. `raft/payloads.go`); фронт имеет `yarn.lock` без `package-lock.json` (ломает `npm ci`); `RaftClusterWidget.tsx` — ~650 стр.; Swagger-спека устарела (24 пути, ~30 реальных эндпоинтов отсутствуют).
- **INFO:** naming — `docker.DockerRepository` против `Repository` у всех остальных.

Хорошее по безопасности: bridge HMAC — `hmac.Equal` (constant-time), 5-мин skew на кросс-кластер, HMAC поверх on-wire байт до декомпрессии; connector-секреты AES-256-GCM с random nonce; JWT в HttpOnly-cookie; refresh хешируется SHA-256 + constant-time compare; cross-cluster deny-list не пускает секреты на хаб; `uplinkOnly` allowlist на приёмнике как defense-in-depth.

---

## 5. Фичи: логи и управление контейнерами по кластеру

### Что уже есть (можно строить)
- **Каталог peer-URL** (`peer_node_advertise`, реплицируется) + неиспользуемая колонка `Capabilities` — идеально под объявление `ops/v1`.
- **Прецедент node-to-node прокси:** `forwardToLeader` → `POST /raft/forward` (но он unauthenticated — не копировать эту модель!).
- **HMAC-хелперы** (`bridge/auth.go`) + **P2P-канал** metricstream с live-swap секретом.
- **Логи процесса:** `GET /logs` (admin, ring 2000, `logbuffer`) — только локально.
- **Логи контейнера:** `GetContainerLogs` (`docker/collector.go:1470`, с healing stale-ID) — только локальный демон; для удалённого хоста хендлер отдаёт `available=false`.
- **Управление контейнерами (start/stop/restart): НЕ существует.** Но collector держит живой Docker SDK client — добавить 3 метода тривиально.
- **Пробел:** нет маппинга «хост → node_id, который его собирает» — без него нельзя ответить «какой peer-URL обслуживает host 5».

### A) Внутри локального кластера — аутентифицированный node-to-node прокси (НЕ command-over-Raft)
Браузер → своя нода (admin JWT) → целевая нода (HMAC поверх intra-канала) → ответ обратно. Raft-путь отвергнут: записи durable и реплеятся (команда restart переисполнится при восстановлении снапшота), Raft one-way (нужен request/response и стриминг логов), проект уже осознанно снял эфемерный трафик с Raft.
- **Роутинг:** добавить `raft_node_id` в `hosts` (payload+applier+миграция+stamp в `RegisterOrUpdateCurrentHost`) → `LookupPeerURL(cluster, nodeID)`. connector-хосты (PVE-гости, PBS) без агента → op «не поддерживается».
- **Node-to-node роуты** (публичные пути, только HMAC — как `/cluster/metrics`, JWT туда не пускать): `GET /cluster/ops/process-logs`, `GET /cluster/ops/container-logs`, `POST /cluster/ops/container-action`, `GET /cluster/ops/container-logs/stream` (SSE, фаза 2).
- **Критично:** текущий `Sign` покрывает только `ts:body` → для GET (пустое тело) подпись = по одному timestamp → реплей на любой HMAC-эндпоинт в окне skew. Нужен **`SignRequest(secret, ts, method, path, query, actor, body)`** до любого мутирующего op + nonce-кэш (TTL 5 мин, bounded).
- **Actor:** `X-Ops-Actor` в MAC → аудит на исполняющей ноде (JWT ноду не пересекает).
- **Офлайн-цель:** проверить `last_seen` (45s) → мгновенный 503, **не** ставить действия в очередь (stop/restart через минуты — footgun).
- **Стриминг:** фаза 1 — tail-fetch (уже есть форма), фаза 2 — SSE pass-through с лимитом сессий/длительности.

### B) Хаб → спок, безопасно (спок outbound-only)
Лидер спока держит **исходящий long-poll** к хабу (`GET /bridge/commands/poll` держится ≤45s), результат постит обратно (`POST /bridge/commands/result`). Хаб-UI кладёт команду в mailbox (admin JWT). Входящих соединений к споку нет никогда. Ускорение: пустое тело ответа metricstream/replicate-POST → добавить `{"commands_pending": true}`.
- **Крипто (хаб не должен держать silent-full-control):** **отдельный ключ, не метрик-секрет.** Асимметрия per-spoke: хаб генерит Ed25519-пару при enrolment, спок хранит только **публичный** ключ → с спока нечего украсть для подписи команд. Каждая команда — Ed25519-envelope с `command_id`, `ttl≤60s`, `target: {cluster, host_mac}`, `action`, `hub_actor`.
- **Таргет по идентичности** (`host_mac`/`external_id`), не по локальному `host_id`.
- **Off by default**, opt-in только через admin-UI спока; хаб-пути физически не могут флипнуть флаг. Жёсткий spoke-enforced allowlist: `process_logs.fetch`, `container_logs.fetch(tail≤5000)`, `container.{start,stop,restart}` — больше ничего не парсится. **Невозможно by construction:** произвольный exec, чтение файлов/env/секретов, мутация config/users, readback connector-секретов, рестарт самого node-stats.
- **Реплей:** `executed_remote_commands (command_id, executed_at)`, тот же паттерн что `applied_remote_log`, TTL-expiry.
- **Ревокация:** тумблер спока off → poller стоп, ключ удалён. Мгновенно, односторонне.
- **Исполнение = переиспользует прокси из A:** B — только транспорт, один путь исполнения, две точки входа.

### Оценка усилий и порядок
- **A ≈ M** (фаза 1 без стриминга реально маленькая — почти всё есть). **B ≈ L**, hard-depends на роутинг+исполнение из A.
- Rollout: (1) A-read: host→node маппинг + `SignRequest` + прокси tail-логов; (2) A-write: start/stop/restart + confirmDialog + аудит; (3) A-stream SSE; (4) B-read: enrolment + канал + hub-initiated tail (только чтение); (5) B-write: действия с хаба после обкатки канала. Каждый шаг отдельно шипается на beta.
- **Риски:** body-only `Sign` (prerequisite, не enhancement); `/raft/forward` уже unauthenticated (не копировать); clock-skew на SBC; лики goroutine на log-follow; логи контейнера могут содержать секреты (informed-consent в диалоге).

---

## Приоритет фиксов

1. **F4** `/raft/forward` без auth — закрыть HMAC (дыра авторизации кластера).
2. **F1** реплицировать user-delete (удалённый юзер живёт на пирах).
3. **F3** rekey cipher коннекторов после join (иначе секрет теряется).
4. **F2** реплицировать refresh-токены (разлогин + невозможность отзыва на пирах).
5. **F5** восстановить panic-контракт FSM (тихая дивергенция БД).
6. **Docs** — переписать `ARCHITECTURE.md`/`CLUSTER.md` под off-Raft метрики (главный источник путаницы для будущих правок).
7. F6/F7/F9/F10/F11/F12/F13 — по мере важности синка.
