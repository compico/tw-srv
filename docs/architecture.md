# Устройство и разработка

## Компоненты

```
 браузер ──► traefik ─┬─ DOMAIN/auth/*  ──► twitch-auth (Twitch OAuth, сессии, refresh токенов Twitch) ── auth.db
                      └─ DOMAIN (остальное) ► memealerts (панель, награды, EventSub, MemeAlerts API,
                                              │          refresh токенов MemeAlerts)            ── memealerts.db
                                              │  gRPC (внутренняя сеть compose, наружу не выходит)
                                              └──────► twitch-auth: ValidateSession, GetAccessToken
```

| Сервис | Отвечает за | Не знает о |
|---|---|---|
| `twitch-auth` | вход через Twitch, cookie-сессии, хранение токенов Twitch и их фоновое обновление | MemeAlerts, наградах |
| `memealerts` | подключение MemeAlerts и фоновое обновление его токенов, награда и её лимиты, приём использований, выдача бонуса, Vue-панель | паролях и refresh-токенах Twitch: просит свежий access token по gRPC |

Базы SQLite раздельные (свои тома). Токены в базах шифруются AES-256-GCM, ключи у сервисов разные.
Вызовы gRPC защищены общим секретом (`GRPC_SHARED_SECRET`), порт наружу не публикуется.

```
cmd/{twitch-auth,memealerts}   точки входа
internal/auth                  домен: модели, репозиторий, сервис, gRPC-сервер, HTTP
internal/support               домен: обратная связь (заявки и переписка), хранится в memealerts.db
internal/authclient            gRPC-клиент к twitch-auth
internal/memealert             домен: модели, репозиторий, сервис, обработка заявок, слушатели
internal/twitch                OAuth, Helix, EventSub (общий код)
internal/platform/*            config, sqlitedb, secretbox, httpx, view (Vite)
proto/auth/v1                  gRPC-контракт
frontend                       Vue 3 + Vite; сборка встраивается в бинарник memealerts (go:embed)
```

## Как это работает

1. Стример открывает панель и нажимает «Войти через Twitch»: `/auth/twitch/login` (сервис `twitch-auth`) → Twitch →
   `/auth/twitch/callback` → панель.
   Дальше он подключает свой MemeAlerts (OAuth, scope `oauth-user-show oauth-bonus-give`): это нужно один раз,
   потому что список саппортеров и начисление мемкоинов доступны только по токену самого стримера.
2. В панели настраивает награду; сервис создаёт её на канале через Helix. Лимиты Twitch:
   **один раз за стрим на зрителя** (`max_per_user_per_stream`), **общая перезарядка** (`global_cooldown`),
   **максимум за стрим** (`max_per_stream`). Лимиты считает сам Twitch, у Twitch нет перезарядки на зрителя.
3. Использования приходят через EventSub по WebSocket (публичный webhook не нужен). После каждого
   (пере)подключения сервис дозабирает заявки в статусе `UNFULFILLED`, чтобы ничего не потерять.
4. Для заявки: ник берётся из поля ввода (или из ника Twitch) → точный поиск среди саппортеров →
   `give-bonus` → заявка `FULFILLED`. Не найден, неоднозначен, заблокирован или API недоступен → `CANCELED` (возврат баллов).

### Токены

- **Twitch** (`twitch-auth`): фоновая горутина раз в минуту обновляет токены, которым осталось мало жить, и раз в час
  проверяет их через `/oauth2/validate`. Обновление идёт под блокировкой пользователя, новый refresh token
  сохраняется сразу. Если Twitch отклонил refresh token, стримеру в панели предлагается войти заново.
- **MemeAlerts** (`memealerts`): токен обновляется по требованию (за 2 минуты до истечения и при ответе 401), а фоновая
  горутина раз в сутки обновляет все подключённые аккаунты. Refresh token MemeAlerts одноразовый и живёт 30 дней;
  фоновое обновление не даёт подключению истечь, если наградой давно не пользовались. Если MemeAlerts отклонил токен,
  аккаунт помечается, панель просит подключить MemeAlerts заново, а использования награды возвращают баллы.
- Токены хранятся в SQLite зашифрованными (AES-256-GCM).

### Защита от двойной выдачи

`give-bonus` не идемпотентен. Поэтому каждая заявка Twitch заносится в журнал (PRIMARY KEY = id заявки)
и переходит в `giving` **до** запроса. Если исход запроса неизвестен (таймаут, 5xx, перезапуск процесса),
бонус **не повторяется и баллы не возвращаются**: заявка получает статус «Нужна проверка» и остаётся в очереди Twitch.
Автоматический возврат только когда точно известно, что бонус не начислен (ответ 4xx).

Лимит MemeAlerts на одну выдачу: 1000 мемкоинов.

## Запуск (кратко)

1. Приложение Twitch: https://dev.twitch.tv/console/apps, Redirect URL `https://<DOMAIN>/auth/twitch/callback`.
2. Приложение MemeAlerts (по документации регистрируется через поддержку), Redirect URI `https://<DOMAIN>/api/memealerts/callback`.
3. `cp .env.example .env`, заполнить; ключи и секрет: `openssl rand -hex 32`.
4. Один раз локально: `make setup` (генерирует gRPC-код, `go mod tidy`) и закоммитьте `go.sum`.
5. `docker compose up -d`. Нужна внешняя сеть Traefik (по умолчанию `proxy`, имя в `TRAEFIK_NETWORK`).

Подробная инструкция по развёртыванию без клонирования репозитория: [docs/deploy.md](docs/deploy.md).

Образы собирает GitHub Actions (`.github/workflows/build.yml`) только при создании тега версии `vX.Y.Z` и публикует их в GHCR:
`ghcr.io/compico/tw-srv-twitch-auth` и `ghcr.io/compico/tw-srv-memealerts`. Для тега `v1.2.3` создаются теги образа `v1.2.3`, `v1.2` и `v1`.
В `docker-compose.yml` указан `:v1`, поэтому `docker compose pull && docker compose up -d` подтягивает последнюю версию 1.x.
Для запуска достаточно `docker-compose.yml` и `.env`. Локальная сборка: `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`.

Выпуск версии: `git tag v1.0.1 && git push origin v1.0.1`.

Правила Traefik: `Host(DOMAIN) && PathPrefix(/auth)` → twitch-auth (приоритет выше), остальное → memealerts. HTTP редиректится на HTTPS (`https-redirect@file`), по HTTPS подключаются `default-headers@file` и certresolver `cloudflare`. Все эти имена переопределяются в `.env`, см. `.env.example`.
Cookie сессии ставит twitch-auth (HttpOnly, Secure, без атрибута Domain), на том же хосте её читает memealerts и проверяет сессию по gRPC. Токены и секреты браузеру не передаются.

## Разработка фронтенда

`docker compose -f docker-compose.yml -f docker-compose.dev.yml up`: Go отдаёт HTML-оболочку,
скрипты и HMR идут с Vite (`localhost:5173`). Без Docker: `cd frontend && npm i && npm run dev`
и `DEV=true` у memealerts. В проде оболочка берёт хэшированные имена из `dist/.vite/manifest.json`.
Добавлять страницы и API можно в `frontend/src` и `internal/memealert/http.go`.

## Поддержка (обратная связь)

Заявка (`support_tickets`) состоит из сообщений (`support_messages`); первое сообщение это описание заявки.
Доступ: автор видит свои заявки, администратор (`ADMIN_TWITCH_ID`) видит все; чужая заявка отвечает 404.
Если переменная пуста, администратора нет.

Статус хранится по сути: `awaiting_staff` (последним написал автор), `awaiting_author` (последним ответил
администратор), `closed`. Участнику он показывается так: «ждёт ответа» это ожидание ответа именно от него,
«открыто» это он уже ответил; для автора и администратора эти два статуса всегда противоположны.
Закрыть заявку может автор или администратор, в закрытую писать нельзя.

У сообщения есть поле `format`: новые сообщения пишутся в `markdown`, ранние остались `text` (простой текст).
Фронтенд отображает каждое по своему формату (`frontend/src/components/MessageBody.vue`).
Маршруты: `/api/support/*` (регистрируются через `memealert.HTTPConfig.Extra`), страницы `/support`.

## Загрузка изображений (API)

Для markdown-сообщений: картинка загружается отдельным запросом, в тексте остаётся только ссылка
(`![](/api/uploads/<id>)`), содержимое в сообщении не хранится.

| Метод и путь | Назначение |
|---|---|
| `GET /api/csrf` | `{token, header}`: токен для заголовка `X-CSRF-Token` |
| `POST /api/uploads/images` | `multipart/form-data`, поле `file`; ответ `201 {id, url, name, contentType, size, createdAt}` |
| `GET /api/uploads` | загрузки текущего пользователя |
| `GET /api/uploads/:id` | сам файл (только для вошедших; id случайный, 128 бит) |
| `DELETE /api/uploads/:id` | удалить (владелец или администратор) |

- Размер до 5 МБ (`413` при превышении). Формат определяется по содержимому: PNG, JPEG, GIF, WebP (`415` для остального;
  SVG не поддерживается из-за скриптов). На пользователя не более 200 файлов и 200 МБ (`422`).
- Владелец (Twitch id) записан в таблице `uploads` базы `memealerts.db`; файлы лежат в `UPLOADS_DIR` (`/data/uploads`,
  в томе `memealerts-data`, поэтому попадают в ту же резервную копию).
- Изменяющие запросы требуют заголовок `X-CSRF-Token`: HMAC от id сессии, ключ выводится из `TOKEN_ENCRYPTION_KEY`
  (отдельной переменной нет). Заголовок `Origin` проверяется по списку: адрес сайта (`PUBLIC_URL`) плюс
  `CORS_ALLOWED_ORIGINS`; для разрешённых источников отвечает CORS-preflight (`credentials: true`, заголовки
  `Content-Type`, `X-CSRF-Token`). Код: `internal/platform/webguard`.
- Лимит тела запроса в приложении 6 МБ (5 МБ файл и поля multipart). Если перед приложением стоит прокси
  с меньшим лимитом, поднимите его.

## Редактор markdown

Заявки и ответы пишутся в [md-editor-v3](https://github.com/imzbf/md-editor-v3)
(`frontend/src/components/MarkdownEditor.vue`, просмотр: `MarkdownView.vue`, общая настройка: `src/markdown/setup.ts`).
Редактор грузится отдельным куском бандла, только на страницах поддержки.

- Загрузка картинок: обработчик `onUploadImg` отправляет каждый файл на `POST /api/uploads/images` с заголовком
  `X-CSRF-Token` (токен из `GET /api/csrf`, при устаревшем токене один повтор) и вставляет в текст полученную ссылку.
  Работают кнопка «Изображение → загрузить файл», вставка из буфера и перетаскивание. Файл больше 5 МБ отклоняется
  ещё в браузере.
- Безопасность отображения: сырой HTML в markdown выключен, готовый HTML дополнительно чистится DOMPurify.
  Разрешены только картинки с наших адресов `/api/uploads/<32 hex>` (внешние картинки раскрыли бы адрес читателя),
  ссылки открываются с `rel="noopener noreferrer nofollow"`, `javascript:` и подобное вырезается.
- Без внешних запросов: по умолчанию библиотека тянет скрипты с unpkg.com. Здесь mermaid, katex, echarts,
  highlight.js и prettier выключены, а cropper (обрезка перед загрузкой) и screenfull подключены из бандла.
  Интерфейс на русском (`language: 'ru-RU'`).

## Долгие запросы

`internal/platform/slowreq`: middleware в обоих сервисах. Выполняет следующий обработчик, а в `defer` пишет в лог
`slow request` (метод, путь без query, статус, длительность), если запрос шёл дольше 15 секунд. Порог:
`slowreq.DefaultThreshold`.

## Push-уведомления администратору

Браузерные уведомления (Web Push, VAPID) о новых заявках и новых ответах пользователей. Только для администратора
(`ADMIN_TWITCH_ID`); свои действия администратора его не уведомляют. Работают при закрытой вкладке.

- Включение: страница «Поддержка» → карточка «Уведомления о заявках» → «Включить на этом устройстве». Браузер запросит
  разрешение, зарегистрирует service worker (`/sw.js`, источник: `frontend/public/sw.js`) и передаст подписку серверу.
  Устройств может быть несколько (до 10), у каждого своя подписка; есть кнопки «Отправить тест» и «Выключить».
- Ключи: `docker compose run --rm memealerts vapid-keys` печатает `VAPID_PUBLIC_KEY` и `VAPID_PRIVATE_KEY`;
  впишите их в `.env` (`VAPID_SUBJECT` необязателен, по умолчанию адрес сайта). Без ключей функция выключена,
  в карточке видна подсказка.
- Сервер: `internal/push` (подписки в таблице `push_subscriptions`, отправка через `webpush-go`), `support.Service`
  вызывает `Notifier.NotifyStaff` после записи заявки или ответа автора; отправка идёт в фоне и запрос не задерживает.
  Подписки, которые push-сервис отвергает (404/410), удаляются автоматически.
- API (`/api/push/*`, только администратор, изменяющие запросы с `X-CSRF-Token`): `GET status`, `POST subscribe`,
  `POST unsubscribe`, `POST test`.
- В тексте уведомления только тема заявки и имя автора, содержимое сообщений не отправляется.
- Ограничения платформ: на iPhone и iPad Web Push работает только у сайта, добавленного на экран «Домой».
  Сервис работает только по HTTPS (Traefik уже так и отдаёт).
