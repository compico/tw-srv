# Развёртывание tw-srv на своём сервере

Клонировать репозиторий не нужно: понадобятся два файла (`docker-compose.yml` и `.env`), образы скачиваются из GHCR.

## Что понадобится

- Сервер с Docker и Docker Compose v2.
- Traefik в Docker с внешней сетью `proxy`, entrypoints `web` и `websecure`, certresolver `cloudflare` и middleware из файловой конфигурации `https-redirect@file` и `default-headers@file`. Это значения по умолчанию, их можно переопределить в `.env`.
- Два домена (или поддомена) одного родительского домена, A/AAAA-записи которых указывают на сервер. Например, `tw.example.com` для входа через Twitch (`twitch-auth`) и `memealerts.example.com` для панели и API (`memealerts`). Родительский домен (`example.com`) нужен для общей cookie сессии.

## 1. Зарегистрируйте приложения

**Twitch**: <https://dev.twitch.tv/console/apps> → Register Your Application.
- OAuth Redirect URL: `https://AUTH_DOMAIN/auth/twitch/callback` (домен сервиса входа, например `tw.example.com`)
- Category: Application Integration, Client Type: Confidential
- Сохраните Client ID и сгенерируйте Client Secret.

**MemeAlerts**: приложение регистрируется через поддержку MemeAlerts (API в режиме тестирования). Укажите:
- Redirect URI: `https://APP_DOMAIN/api/memealerts/callback` (домен панели, например `memealerts.example.com`)
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
| `AUTH_DOMAIN` | домен сервиса входа без схемы, например `tw.example.com` |
| `APP_DOMAIN` | домен панели и API без схемы, например `memealerts.example.com` |
| `COOKIE_DOMAIN` | общий родительский домен для cookie сессии, например `example.com`. Без него вход не заработает: сессию не увидит второй сервис |
| `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET` | из приложения Twitch |
| `MEMEALERTS_CLIENT_ID`, `MEMEALERTS_CLIENT_SECRET` | из приложения MemeAlerts |
| `AUTH_ENCRYPTION_KEY`, `MEMEALERTS_ENCRYPTION_KEY` | ключи шифрования токенов в базах, у каждого сервиса свой |
| `GRPC_SHARED_SECRET` | секрет для связи сервисов между собой |
| `TRAEFIK_NETWORK`, `TRAEFIK_ENTRYPOINT_HTTP`, `TRAEFIK_ENTRYPOINT`, `TRAEFIK_CERTRESOLVER`, `TRAEFIK_MIDDLEWARES`, `TRAEFIK_REDIRECT_MIDDLEWARE` | необязательные, имена из вашей конфигурации Traefik, если они отличаются от значений по умолчанию (`proxy`, `web`, `websecure`, `cloudflare`, `default-headers@file`, `https-redirect@file`) |

Ключи и секрет генерируются так (по одному значению на каждую переменную):

```bash
openssl rand -hex 32
```

> **Сохраните ключи шифрования.** Если потерять `AUTH_ENCRYPTION_KEY` или `MEMEALERTS_ENCRYPTION_KEY`, сохранённые токены станут нечитаемыми и всем стримерам придётся авторизоваться заново.

## 4. Запустите

```bash
docker network ls | grep proxy        # сеть Traefik должна существовать
docker compose pull
docker compose up -d
docker compose logs -f
```

В логах должны появиться `http listening` у обоих сервисов. Откройте `https://APP_DOMAIN`: должна показаться кнопка «Войти через Twitch». Она ведёт на `AUTH_DOMAIN`, а после входа возвращает на `APP_DOMAIN`.

gRPC-порт (`9090`) наружу не публикуется, он доступен только внутри сети compose.

## Как пользоваться

1. Стример открывает `https://APP_DOMAIN`, входит через Twitch и подключает свой MemeAlerts (один раз).
2. Настраивает награду: цену в баллах, сколько мемкоинов выдавать, лимиты (раз за стрим на зрителя, общая перезарядка, максимум за стрим) и откуда брать ник. Награда создаётся на его канале Twitch.
3. Зритель использует награду и вводит ник MemeAlerts. Если он есть среди саппортеров этого стримера, ему начисляются мемкоины, иначе баллы возвращаются.
4. История использований и причины возвратов видны в панели.

Токены Twitch и MemeAlerts обновляются сами в фоне. Если стримера просят «войти заново» или «подключить MemeAlerts заново», значит соответствующий сервис отозвал доступ, и это делается одной кнопкой в панели.

## Обновление

```bash
docker compose pull
docker compose up -d
```

Образы собираются при выпуске версии (тег `vX.Y.Z` в репозитории). Для версии `v1.2.3` публикуются теги образа `v1.2.3`, `v1.2` и `v1`. В `docker-compose.yml` указан `:v1`, поэтому команды выше всегда подтягивают последнюю версию 1.x.

Чтобы остаться на конкретной версии, замените `:v1` в `docker-compose.yml` на `:v1.2` (только исправления 1.2.x) или `:v1.2.3` (точная версия). Откат делается так же: верните прежний тег и выполните `docker compose up -d`.

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
| `network proxy declared as external, but could not be found` | Создайте сеть (`docker network create proxy`) или задайте её имя в `TRAEFIK_NETWORK` |
| Twitch: `redirect_mismatch` | Redirect URL в приложении Twitch должен точно совпадать с `https://AUTH_DOMAIN/auth/twitch/callback` |
| После входа через Twitch снова просит войти | Проверьте `COOKIE_DOMAIN`: это должен быть общий родитель `AUTH_DOMAIN` и `APP_DOMAIN` (для `tw.example.com` и `memealerts.example.com` это `example.com`). Сайт должен открываться по HTTPS |
| В панели «Сервис авторизации недоступен» | Проверьте `docker compose logs twitch-auth` и что `GRPC_SHARED_SECRET` одинаковый у обоих сервисов (он берётся из одного `.env`) |
| Награду не удаётся создать | Награды за баллы канала доступны только аффилиатам и партнёрам Twitch |
| Заявка в статусе «Нужна проверка» | Исход начисления мемкоинов неизвестен. Проверьте баланс зрителя в MemeAlerts вручную. Заявка осталась в очереди Twitch: закройте или отклоните её там |
