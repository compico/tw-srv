# Развёртывание tw-srv

Пошаговая инструкция для запуска на своём сервере. Репозиторий клонировать не нужно: достаточно `docker-compose.yml` и `.env`, образы скачиваются из GHCR.

Ниже `DOMAIN` — домен сервиса (например, `memealerts.example.com`). Оба сервиса работают на нём: Traefik отправляет `/auth/*` в `twitch-auth`, остальное в `memealerts`.

## Чек-лист

- [ ] Docker и Docker Compose v2 на сервере
- [ ] Traefik в Docker: внешняя сеть `proxy`, entrypoints `web` и `websecure`, certresolver `cloudflare`, middleware `https-redirect@file` и `default-headers@file` (другие имена задаются в `.env`)
- [ ] DNS: A/AAAA-запись `DOMAIN` на сервер
- [ ] Приложение Twitch
- [ ] Приложение MemeAlerts
- [ ] `.env` заполнен
- [ ] `docker compose up -d`

## 1. Приложение Twitch

<https://dev.twitch.tv/console/apps> → Register Your Application:

- OAuth Redirect URL: `https://DOMAIN/auth/twitch/callback`
- Category: Application Integration
- Client Type: Confidential

Сохраните Client ID и создайте Client Secret.

## 2. Приложение MemeAlerts

Регистрируется через поддержку MemeAlerts (или `POST /api/oauth/apps`, если доступно для аккаунта):

- Redirect URI: `https://DOMAIN/api/memealerts/callback`
- Scopes: `oauth-user-show oauth-bonus-give`

Сохраните `clientId` и `clientSecret` (секрет показывается один раз).

## 3. Файлы

```bash
mkdir tw-srv && cd tw-srv
curl -fsSLO https://raw.githubusercontent.com/compico/tw-srv/main/docker-compose.yml
curl -fsSL  https://raw.githubusercontent.com/compico/tw-srv/main/.env.example -o .env
chmod 600 .env
```

## 4. `.env`

Секреты генерируются одной командой:

```bash
for k in AUTH_ENCRYPTION_KEY MEMEALERTS_ENCRYPTION_KEY GRPC_SHARED_SECRET; do
  sed -i "s|^$k=.*|$k=$(openssl rand -hex 32)|" .env
done
```

Остальное впишите вручную:

| Переменная | Значение |
|---|---|
| `DOMAIN` | домен сервиса без схемы |
| `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET` | из шага 1 |
| `MEMEALERTS_CLIENT_ID`, `MEMEALERTS_CLIENT_SECRET` | из шага 2 |
| `TRAEFIK_*` | необязательные; имена сети, entrypoints, certresolver и middleware, если у вас они другие. Значения по умолчанию: `proxy`, `web`, `websecure`, `cloudflare`, `default-headers@file`, `https-redirect@file` |

> Сохраните `AUTH_ENCRYPTION_KEY` и `MEMEALERTS_ENCRYPTION_KEY`. Без них токены в базах не расшифровать, и стримерам придётся авторизоваться заново.

## 5. Запуск

```bash
docker network ls | grep proxy   # сеть Traefik существует
docker compose pull
docker compose up -d
docker compose logs -f
```

## 6. Проверка

- [ ] В логах обоих сервисов есть `http listening`
- [ ] `https://DOMAIN` открывается, показана кнопка «Войти через Twitch»
- [ ] Вход проходит: Twitch → возврат на `DOMAIN`, видна панель
- [ ] «Подключить MemeAlerts» проходит и возвращает в панель
- [ ] Награда создаётся и появляется в списке наград канала Twitch (нужен аффилиат или партнёр)

gRPC-порт `9090` наружу не публикуется, он доступен только внутри сети compose.

## Обновление

```bash
docker compose pull
docker compose up -d
```

Образы собираются при выпуске версии (тег `vX.Y.Z` в репозитории). Для `v1.2.3` публикуются теги образа `v1.2.3`, `v1.2` и `v1`. В `docker-compose.yml` указан `:v1`: команды выше подтягивают последнюю версию 1.x.

Закрепить версию или откатиться: замените `:v1` в `docker-compose.yml` на `:v1.2` или `:v1.2.3` и выполните `docker compose up -d`.

## Резервная копия

Данные лежат в томах `tw-srv_auth-data` (сессии и токены Twitch) и `tw-srv_memealerts-data` (подключения MemeAlerts, награды, журнал):

```bash
docker compose stop
for v in auth-data memealerts-data; do
  docker run --rm -v tw-srv_$v:/data -v "$PWD":/backup alpine tar czf /backup/$v.tgz -C /data .
done
docker compose start
```

Храните копию вместе с `.env`: без ключей шифрования она бесполезна.

## Если что-то не работает

| Симптом | Причина и решение |
|---|---|
| `pull access denied` / `unauthorized` | Образы в GHCR приватные: сделайте пакеты публичными (Packages → пакет → Package settings → Change visibility) или выполните `docker login ghcr.io` |
| `network proxy declared as external, but could not be found` | `docker network create proxy` или имя сети в `TRAEFIK_NETWORK` |
| `required variable ... is missing a value` | Не заполнена переменная в `.env` (`DOMAIN`) |
| Twitch: `redirect_mismatch` | Redirect URL в приложении Twitch не совпадает с `https://DOMAIN/auth/twitch/callback` |
| MemeAlerts: ошибка redirect URI | Redirect URI приложения не совпадает с `https://DOMAIN/api/memealerts/callback` |
| После входа через Twitch снова просит войти | Сайт открыт не по HTTPS (cookie сессии помечена Secure) |
| В панели «сервис авторизации недоступен» | `docker compose logs twitch-auth`; `GRPC_SHARED_SECRET` должен быть общим (он берётся из одного `.env`) |
| Награду не удаётся создать | Награды за баллы канала доступны только аффилиатам и партнёрам Twitch |
| Заявка в статусе «Нужна проверка» | Исход начисления неизвестен: проверьте баланс зрителя в MemeAlerts. Заявка осталась в очереди Twitch: закройте или отклоните её там |
