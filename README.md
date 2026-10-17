# tw-srv: мемкоины за баллы канала

Зритель тратит баллы канала на награду «Получить мемкоины», сервис находит его среди саппортеров
стримера в MemeAlerts и начисляет бонус. Нет в списке саппортеров или ошибка: баллы возвращаются.

## Устройство

```
 браузер ──► traefik ─┬─ /auth/*  ──► twitch-auth  (Twitch OAuth, сессии, refresh токенов)  ── auth.db
                      └─ всё остальное ► memealerts (панель, награды, EventSub, MemeAlerts API) ── memealerts.db
                                              │  gRPC (внутренняя сеть compose)
                                              └──────► twitch-auth: ValidateSession, GetAccessToken
```

| Сервис | Отвечает за | Не знает о |
|---|---|---|
| `twitch-auth` | вход через Twitch, cookie-сессии, хранение и обновление токенов Twitch | MemeAlerts, наградах |
| `memealerts` | подключение MemeAlerts, награда и её лимиты, приём использований, выдача бонуса, Vue-панель | паролях и refresh-токенах Twitch: просит свежий access token по gRPC |

Базы SQLite раздельные (свои тома). Токены в базах шифруются AES-256-GCM, ключи у сервисов разные.
Вызовы gRPC защищены общим секретом (`GRPC_SHARED_SECRET`), порт наружу не публикуется.

```
cmd/{twitch-auth,memealerts}   точки входа
internal/auth                  домен: модели, репозиторий, сервис, gRPC-сервер, HTTP
internal/authclient            gRPC-клиент к twitch-auth
internal/memealert             домен: модели, репозиторий, сервис, обработка заявок, слушатели
internal/twitch                OAuth, Helix, EventSub (общий код)
internal/platform/*            config, sqlitedb, secretbox, httpx, view (Vite)
proto/auth/v1                  gRPC-контракт
frontend                       Vue 3 + Vite; сборка встраивается в бинарник memealerts (go:embed)
```

## Как это работает

1. Стример входит через Twitch (`/auth/twitch/login`) и подключает MemeAlerts (OAuth, scope `oauth-user-show oauth-bonus-give`).
2. В панели настраивает награду; сервис создаёт её на канале через Helix. Лимиты Twitch:
   **один раз за стрим на зрителя** (`max_per_user_per_stream`), **общая перезарядка** (`global_cooldown`),
   **максимум за стрим** (`max_per_stream`). Лимиты считает сам Twitch, у Twitch нет перезарядки на зрителя.
3. Использования приходят через EventSub по WebSocket (публичный webhook не нужен). После каждого
   (пере)подключения сервис дозабирает заявки в статусе `UNFULFILLED`, чтобы ничего не потерять.
4. Для заявки: ник берётся из поля ввода (или из ника Twitch) → точный поиск среди саппортеров →
   `give-bonus` → заявка `FULFILLED`. Не найден, неоднозначен, заблокирован или API недоступен → `CANCELED` (возврат баллов).

### Защита от двойной выдачи

`give-bonus` не идемпотентен. Поэтому каждая заявка Twitch заносится в журнал (PRIMARY KEY = id заявки)
и переходит в `giving` **до** запроса. Если исход запроса неизвестен (таймаут, 5xx, перезапуск процесса),
бонус **не повторяется и баллы не возвращаются**: заявка получает статус «Нужна проверка» и остаётся в очереди Twitch.
Автоматический возврат только когда точно известно, что бонус не начислен (ответ 4xx).

Лимит MemeAlerts на одну выдачу: 1000 мемкоинов.

## Запуск

1. Приложение Twitch: https://dev.twitch.tv/console/apps, Redirect URL `https://<DOMAIN>/auth/twitch/callback`.
2. Приложение MemeAlerts (по документации регистрируется через поддержку), Redirect URI `https://<DOMAIN>/api/memealerts/callback`.
3. `cp .env.example .env`, заполнить; ключи и секрет: `openssl rand -hex 32`.
4. Один раз локально: `make setup` (генерирует gRPC-код, `go mod tidy`) и закоммитьте `go.sum`.
5. `docker compose up -d`. Нужна внешняя сеть traefik (`docker network create traefik`, имя в `TRAEFIK_NETWORK`).

Подробная инструкция по развёртыванию без клонирования репозитория: [docs/deploy.md](docs/deploy.md).

Образы собирает GitHub Actions (`.github/workflows/build.yml`) при каждом пуше в `main` и публикует в GHCR:
`ghcr.io/compico/tw-srv-twitch-auth` и `ghcr.io/compico/tw-srv-memealerts` (теги `latest`, `sha-xxxxxxx`, при теге `vX.Y.Z` ещё версия).
Для запуска достаточно `docker-compose.yml` и `.env`. Обновление: `docker compose pull && docker compose up -d`.
Закрепить версию: `TAG=sha-xxxxxxx` в `.env`. Локальная сборка: `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`.

Правила Traefik: `Host(DOMAIN) && PathPrefix(/auth)` → twitch-auth (приоритет выше), остальное → memealerts.
Cookie сессии ставится на домен без атрибута Domain, поэтому оба сервиса должны жить на одном хосте.

## Разработка фронтенда

`docker compose -f docker-compose.yml -f docker-compose.dev.yml up`: Go отдаёт HTML-оболочку,
скрипты и HMR идут с Vite (`localhost:5173`). Без Docker: `cd frontend && npm i && npm run dev`
и `DEV=true` у memealerts. В проде оболочка берёт хэшированные имена из `dist/.vite/manifest.json`.
Добавлять страницы и API можно в `frontend/src` и `internal/memealert/http.go`.
