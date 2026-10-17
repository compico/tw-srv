# Развёртывание tw-srv на своём сервере

Клонировать репозиторий не нужно: понадобятся два файла (`docker-compose.yml` и `.env`), образы скачиваются из GHCR.

## Что понадобится

- Сервер с Docker и Docker Compose v2.
- Traefik в Docker с HTTPS и внешней сетью (по умолчанию `traefik`, entrypoint `websecure`, certresolver `letsencrypt`).
- Домен, A/AAAA-запись которого указывает на сервер. Оба сервиса работают на одном домене: Traefik отправляет `/auth/*` в `twitch-auth`, остальное в `memealerts`.

## 1. Зарегистрируйте приложения

**Twitch**: <https://dev.twitch.tv/console/apps> → Register Your Application.
- OAuth Redirect URL: `https://ВАШ_ДОМЕН/auth/twitch/callback`
- Category: Application Integration, Client Type: Confidential
- Сохраните Client ID и сгенерируйте Client Secret.

**MemeAlerts**: приложение регистрируется через поддержку MemeAlerts (API в режиме тестирования). Укажите:
- Redirect URI: `https://ВАШ_ДОМЕН/api/memealerts/callback`
- Scopes: `oauth-user-show oauth-bonus-give`
- Получите `clientId` и `clientSecret` (секрет показывается один раз).

## 2. Скачайте файлы

```bash
mkdir tw-srv && cd tw-srv
curl -fsSLO https://raw.githubusercontent.com/compico/tw-srv/main/docker-compose.yml
curl -fsSL  https://raw.githubusercontent.com/compico/tw-srv/main/.env.example -o .env
```

## 3. Заполните `.env`

| Переменная | Значение |
|---|---|
| `DOMAIN` | домен без схемы, например `memes.example.com` |
| `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET` | из приложения Twitch |
| `MEMEALERTS_CLIENT_ID`, `MEMEALERTS_CLIENT_SECRET` | из приложения MemeAlerts |
| `AUTH_ENCRYPTION_KEY`, `MEMEALERTS_ENCRYPTION_KEY` | ключи шифрования токенов в базах, у каждого сервиса свой |
| `GRPC_SHARED_SECRET` | секрет для связи сервисов между собой |
| `TRAEFIK_NETWORK`, `TRAEFIK_ENTRYPOINT`, `TRAEFIK_CERTRESOLVER` | имена из вашей конфигурации Traefik, если они отличаются от значений по умолчанию |
| `TAG` | тег образов, `latest` по умолчанию |

Ключи и секрет генерируются так (по одному значению на каждую переменную):

```bash
openssl rand -hex 32
```

> **Сохраните ключи шифрования.** Если потерять `AUTH_ENCRYPTION_KEY` или `MEMEALERTS_ENCRYPTION_KEY`, сохранённые токены станут нечитаемыми и всем стримерам придётся авторизоваться заново.

## 4. Запустите

```bash
docker network ls | grep traefik      # сеть Traefik должна существовать
docker compose pull
docker compose up -d
docker compose logs -f
```

В логах должны появиться `http listening` у обоих сервисов. Откройте `https://ВАШ_ДОМЕН`: должна показаться кнопка «Войти через Twitch».

gRPC-порт (`9090`) наружу не публикуется, он доступен только внутри сети compose.

## Обновление

```bash
docker compose pull
docker compose up -d
```

Образы собираются автоматически при каждом пуше в `main`. Чтобы закрепить версию, укажите в `.env` `TAG=sha-xxxxxxx` (короткий хэш коммита) или `TAG=1.2.3` (для релизов с тегом `v1.2.3`). Откат делается так же, возвратом прежнего `TAG`.

## Резервная копия

Данные лежат в двух томах: `tw-srv_auth-data` (сессии и токены Twitch) и `tw-srv_memealerts-data` (подключения MemeAlerts, награды, журнал). Копия:

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
| `pull access denied` / `unauthorized` при `docker compose pull` | Образы в GHCR приватные. Владелец репозитория делает пакеты публичными (Packages → пакет → Package settings → Change visibility) либо вы выполняете `docker login ghcr.io` |
| `network traefik declared as external, but could not be found` | Создайте сеть (`docker network create traefik`) или задайте её имя в `TRAEFIK_NETWORK` |
| Twitch: `redirect_mismatch` | Redirect URL в приложении Twitch должен точно совпадать с `https://ВАШ_ДОМЕН/auth/twitch/callback` |
| После входа через Twitch снова просит войти | Сайт открыт по `http`, а cookie помечена Secure. Используйте HTTPS. Оба сервиса должны быть на одном домене |
| В панели «Сервис авторизации недоступен» | Проверьте `docker compose logs twitch-auth` и что `GRPC_SHARED_SECRET` одинаковый у обоих сервисов (он берётся из одного `.env`) |
| Награду не удаётся создать | Награды за баллы канала доступны только аффилиатам и партнёрам Twitch |
| Заявка в статусе «Нужна проверка» | Исход начисления мемкоинов неизвестен. Проверьте баланс зрителя в MemeAlerts вручную. Заявка осталась в очереди Twitch: закройте или отклоните её там |
